package vm

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// MultiHostManager routes vm.Manager calls across several per-host
// managers (roadmap 5.5). Architecture: one coordinator daemon holding a
// libvirt connection per host — the implicit "local" host plus every
// `hosts:` config entry — with placement decided at create time via
// VMSpec.Host and recorded on the stored spec. See docs/MULTI_HOST.md for
// the full design (v1 assumes shared storage across hosts).
type MultiHostManager struct {
	hosts       map[string]Manager
	order       []string // deterministic iteration order, default first
	defaultName string

	probeMu    sync.Mutex
	probeCache map[string]hostProbe
}

// hostProbe caches a HostReachable result so repeated Dashboard polls do
// not re-probe every host connection on every request.
type hostProbe struct {
	reachable bool
	checkedAt time.Time
}

// hostProbeTTL bounds how stale a cached reachability verdict may be.
const hostProbeTTL = 5 * time.Second

// Pinger is an optional Manager capability: a cheap liveness probe that
// avoids a full fleet enumeration. LibvirtManager implements it with a
// single GetLibVersion RPC.
type Pinger interface {
	Ping(ctx context.Context) error
}

// NewMultiHostManager builds a router over the given per-host managers.
// defaultName must be a key of hosts; it receives VMs whose spec leaves
// the host empty.
func NewMultiHostManager(defaultName string, hosts map[string]Manager) (*MultiHostManager, error) {
	if _, ok := hosts[defaultName]; !ok {
		return nil, fmt.Errorf("default host %q is not among the configured hosts", defaultName)
	}
	order := make([]string, 0, len(hosts))
	for name := range hosts {
		if name != defaultName {
			order = append(order, name)
		}
	}
	sort.Strings(order)
	order = append([]string{defaultName}, order...)
	return &MultiHostManager{
		hosts:       hosts,
		order:       order,
		defaultName: defaultName,
		probeCache:  make(map[string]hostProbe),
	}, nil
}

// HostNames returns the managed host names, default first.
func (m *MultiHostManager) HostNames() []string {
	return append([]string(nil), m.order...)
}

// DefaultHostName returns the placement default.
func (m *MultiHostManager) DefaultHostName() string { return m.defaultName }

// HostReachable reports whether the named host's manager is currently
// alive. Managers implementing Pinger get a single cheap RPC probe;
// others fall back to a List call. Verdicts are cached for hostProbeTTL
// so repeated Dashboard polls of /hosts don't restorm every connection.
func (m *MultiHostManager) HostReachable(ctx context.Context, name string) bool {
	mgr, ok := m.hosts[name]
	if !ok {
		return false
	}

	m.probeMu.Lock()
	if p, ok := m.probeCache[name]; ok && time.Since(p.checkedAt) < hostProbeTTL {
		m.probeMu.Unlock()
		return p.reachable
	}
	m.probeMu.Unlock()

	var err error
	if pinger, ok := mgr.(Pinger); ok {
		err = pinger.Ping(ctx)
	} else {
		_, err = mgr.List(ctx)
	}
	reachable := err == nil

	m.probeMu.Lock()
	m.probeCache[name] = hostProbe{reachable: reachable, checkedAt: time.Now()}
	m.probeMu.Unlock()
	return reachable
}

// hostFor normalises a spec/stored host name to a routing key.
func (m *MultiHostManager) hostFor(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return m.defaultName
	}
	return host
}

// manager returns the per-host manager for a (possibly empty) host name.
func (m *MultiHostManager) manager(host string) (Manager, error) {
	name := m.hostFor(host)
	mgr, ok := m.hosts[name]
	if !ok {
		return nil, types.NewAPIError("invalid_host", fmt.Sprintf("host %q is not configured (known hosts: %s)", name, strings.Join(m.order, ", ")))
	}
	return mgr, nil
}

// locate finds the VM record and the manager responsible for it. The
// default host is consulted first (in production every host shares the
// metadata store, so the first Get succeeds and the stored spec.host
// routes the call); remaining hosts are scanned as a fallback so
// isolated-store implementations (tests) still resolve.
func (m *MultiHostManager) locate(ctx context.Context, id string) (Manager, *types.VM, error) {
	var lastErr error
	for _, name := range m.order {
		vmRec, err := m.hosts[name].Get(ctx, id)
		if err != nil {
			lastErr = err
			continue
		}
		owner, err := m.manager(vmRec.Spec.Host)
		if err != nil {
			// Stored host no longer configured — fall back to the manager
			// that resolved the record so lifecycle ops keep working.
			return m.hosts[name], vmRec, nil
		}
		return owner, vmRec, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("vms/%s: not found", id)
	}
	return nil, nil, lastErr
}

// --- placement-aware entry points ---

func (m *MultiHostManager) Create(ctx context.Context, spec types.VMSpec) (*types.VM, error) {
	mgr, err := m.manager(spec.Host)
	if err != nil {
		return nil, err
	}
	spec.Host = m.hostFor(spec.Host)
	return mgr.Create(ctx, spec)
}

func (m *MultiHostManager) Clone(ctx context.Context, sourceID string, newName string) (*types.VM, error) {
	mgr, _, err := m.locate(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	return mgr.Clone(ctx, sourceID, newName)
}

func (m *MultiHostManager) List(ctx context.Context) ([]*types.VM, error) {
	// Fan out concurrently so one slow remote doesn't serialise the whole
	// coordinator dashboard behind it.
	type hostResult struct {
		vms []*types.VM
		err error
	}
	results := make([]hostResult, len(m.order))
	var wg sync.WaitGroup
	for i := range m.order {
		wg.Add(1)
		go func(i int, mgr Manager) {
			defer wg.Done()
			vms, err := mgr.List(ctx)
			results[i] = hostResult{vms: vms, err: err}
		}(i, m.hosts[m.order[i]])
	}
	wg.Wait()

	// owner maps a stored host name to the host whose listing carries the
	// row. VMs whose stored host is no longer configured route to the
	// default host, matching locate()'s orphan fallback so Get and List
	// agree on membership.
	owner := func(storedHost string) string {
		name := m.hostFor(storedHost)
		if _, known := m.hosts[name]; !known {
			return m.defaultName
		}
		return name
	}

	var out []*types.VM
	seen := make(map[string]bool)
	failed := make(map[string]bool)
	var firstErr error
	for i, name := range m.order {
		r := results[i]
		if r.err != nil {
			failed[name] = true
			if firstErr == nil {
				firstErr = r.err
			}
			logger.Warn("vm", "multi-host list: host unreachable; falling back to stored state for its VMs",
				"host", name, "error", r.err.Error())
			continue
		}
		for _, vmRec := range r.vms {
			// Every host shares the store in production, so each List
			// returns the whole fleet; keep only the rows this host owns
			// (its libvirt connection enriched their live state), which
			// also yields each VM exactly once across the fan-out.
			if owner(vmRec.Spec.Host) != name {
				continue
			}
			out = append(out, vmRec)
			seen[vmRec.ID] = true
		}
	}
	if len(failed) == len(m.order) {
		return nil, firstErr
	}

	// Backfill rows owned by unreachable hosts from the first healthy
	// host's listing: hosts share the metadata store, so a healthy host's
	// List still returns the stored records (name / spec / last known
	// state) for the down host's VMs. Ops on them will fail cleanly when
	// routed, but the fleet view stays complete.
	if len(failed) > 0 {
		for i := range m.order {
			r := results[i]
			if r.err != nil {
				continue
			}
			for _, vmRec := range r.vms {
				if failed[owner(vmRec.Spec.Host)] && !seen[vmRec.ID] {
					out = append(out, vmRec)
					seen[vmRec.ID] = true
				}
			}
			break // one healthy listing carries the whole shared store
		}
	}
	return out, nil
}

func (m *MultiHostManager) Get(ctx context.Context, id string) (*types.VM, error) {
	mgr, _, err := m.locate(ctx, id)
	if err != nil {
		return nil, err
	}
	return mgr.Get(ctx, id)
}

// --- simple routed lifecycle ops ---

func (m *MultiHostManager) routed(ctx context.Context, id string, fn func(Manager) error) error {
	mgr, _, err := m.locate(ctx, id)
	if err != nil {
		return err
	}
	return fn(mgr)
}

func (m *MultiHostManager) Update(ctx context.Context, id string, patch types.VMUpdateSpec) (*types.VM, error) {
	mgr, _, err := m.locate(ctx, id)
	if err != nil {
		return nil, err
	}
	return mgr.Update(ctx, id, patch)
}

func (m *MultiHostManager) Start(ctx context.Context, id string) error {
	return m.routed(ctx, id, func(mgr Manager) error { return mgr.Start(ctx, id) })
}
func (m *MultiHostManager) Stop(ctx context.Context, id string) error {
	return m.routed(ctx, id, func(mgr Manager) error { return mgr.Stop(ctx, id) })
}
func (m *MultiHostManager) ForceStop(ctx context.Context, id string) error {
	return m.routed(ctx, id, func(mgr Manager) error { return mgr.ForceStop(ctx, id) })
}
func (m *MultiHostManager) Restart(ctx context.Context, id string) error {
	return m.routed(ctx, id, func(mgr Manager) error { return mgr.Restart(ctx, id) })
}
func (m *MultiHostManager) Reboot(ctx context.Context, id string) error {
	return m.routed(ctx, id, func(mgr Manager) error { return mgr.Reboot(ctx, id) })
}
func (m *MultiHostManager) Suspend(ctx context.Context, id string) error {
	return m.routed(ctx, id, func(mgr Manager) error { return mgr.Suspend(ctx, id) })
}
func (m *MultiHostManager) Resume(ctx context.Context, id string) error {
	return m.routed(ctx, id, func(mgr Manager) error { return mgr.Resume(ctx, id) })
}
func (m *MultiHostManager) Delete(ctx context.Context, id string) error {
	return m.routed(ctx, id, func(mgr Manager) error { return mgr.Delete(ctx, id) })
}

// --- snapshots ---

func (m *MultiHostManager) CreateSnapshot(ctx context.Context, vmID string, spec types.SnapshotSpec) (*types.Snapshot, error) {
	mgr, _, err := m.locate(ctx, vmID)
	if err != nil {
		return nil, err
	}
	return mgr.CreateSnapshot(ctx, vmID, spec)
}
func (m *MultiHostManager) UpdateSnapshot(ctx context.Context, vmID string, snapshotName string, patch types.SnapshotUpdateSpec) (*types.Snapshot, error) {
	mgr, _, err := m.locate(ctx, vmID)
	if err != nil {
		return nil, err
	}
	return mgr.UpdateSnapshot(ctx, vmID, snapshotName, patch)
}
func (m *MultiHostManager) RestoreSnapshot(ctx context.Context, vmID string, snapshotName string) error {
	return m.routed(ctx, vmID, func(mgr Manager) error { return mgr.RestoreSnapshot(ctx, vmID, snapshotName) })
}
func (m *MultiHostManager) ListSnapshots(ctx context.Context, vmID string) ([]*types.Snapshot, error) {
	mgr, _, err := m.locate(ctx, vmID)
	if err != nil {
		return nil, err
	}
	return mgr.ListSnapshots(ctx, vmID)
}
func (m *MultiHostManager) DeleteSnapshot(ctx context.Context, vmID string, snapshotName string) error {
	return m.routed(ctx, vmID, func(mgr Manager) error { return mgr.DeleteSnapshot(ctx, vmID, snapshotName) })
}

// --- console + GPU lifecycle ---

func (m *MultiHostManager) GetConsoleEndpoint(ctx context.Context, id string, intent types.ConsoleIntent) (*types.ConsoleEndpoint, error) {
	mgr, _, err := m.locate(ctx, id)
	if err != nil {
		return nil, err
	}
	return mgr.GetConsoleEndpoint(ctx, id, intent)
}

func (m *MultiHostManager) OpenSerialConsole(ctx context.Context, id string) (io.ReadWriteCloser, error) {
	mgr, _, err := m.locate(ctx, id)
	if err != nil {
		return nil, err
	}
	return mgr.OpenSerialConsole(ctx, id)
}

func (m *MultiHostManager) AttachGPU(ctx context.Context, id string, pciAddr string, force bool) (*types.VM, error) {
	mgr, _, err := m.locate(ctx, id)
	if err != nil {
		return nil, err
	}
	return mgr.AttachGPU(ctx, id, pciAddr, force)
}

func (m *MultiHostManager) DetachGPU(ctx context.Context, id string, pciAddr string) (*types.VM, error) {
	mgr, _, err := m.locate(ctx, id)
	if err != nil {
		return nil, err
	}
	return mgr.DetachGPU(ctx, id, pciAddr)
}

// SetConsoleSessionTerminator fans the hook out to every per-host manager
// that supports it.
func (m *MultiHostManager) SetConsoleSessionTerminator(fn ConsoleSessionTerminator) {
	for _, mgr := range m.hosts {
		if setter, ok := mgr.(ConsoleSessionTerminatorSetter); ok {
			setter.SetConsoleSessionTerminator(fn)
		}
	}
}

// Close closes every per-host manager, returning the first error.
func (m *MultiHostManager) Close() error {
	var firstErr error
	for _, name := range m.order {
		if err := m.hosts[name].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// interface conformance
var _ Manager = (*MultiHostManager)(nil)
var _ io.Closer = (*MultiHostManager)(nil)
