package vm

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/store"
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
	return &MultiHostManager{hosts: hosts, order: order, defaultName: defaultName}, nil
}

// HostNames returns the managed host names, default first.
func (m *MultiHostManager) HostNames() []string {
	return append([]string(nil), m.order...)
}

// DefaultHostName returns the placement default.
func (m *MultiHostManager) DefaultHostName() string { return m.defaultName }

// HostReachable reports whether the named host's manager currently
// responds to a List call — the cheapest liveness probe every Manager
// implementation supports.
func (m *MultiHostManager) HostReachable(ctx context.Context, name string) bool {
	mgr, ok := m.hosts[name]
	if !ok {
		return false
	}
	_, err := mgr.List(ctx)
	return err == nil
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
	var out []*types.VM
	var firstErr error
	for _, name := range m.order {
		vms, err := m.hosts[name].List(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, vmRec := range vms {
			// Every host shares the store in production, so each List
			// returns the whole fleet; keep only the rows this host owns
			// (its libvirt connection enriched their live state), which
			// also yields each VM exactly once across the fan-out.
			if m.hostFor(vmRec.Spec.Host) != name {
				continue
			}
			out = append(out, vmRec)
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
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

// NewMultiHostManagerFromConfig wires the production topology: the local
// libvirt host plus one LibvirtManager per `hosts:` entry, all sharing the
// same metadata store (shared storage assumed — see docs/MULTI_HOST.md).
func NewMultiHostManagerFromConfig(cfg *config.Config, s *store.Store) (*MultiHostManager, error) {
	if err := cfg.ValidateHosts(); err != nil {
		return nil, err
	}
	local, err := NewLibvirtManager(cfg, s)
	if err != nil {
		return nil, err
	}
	hosts := map[string]Manager{config.LocalHostName: local}
	for _, h := range cfg.Hosts {
		remoteCfg := *cfg
		remoteCfg.Libvirt.URI = h.URI
		remote, err := NewLibvirtManager(&remoteCfg, s)
		if err != nil {
			// Fail fast: a misconfigured host should surface at daemon
			// startup, not at first placement.
			for _, mgr := range hosts {
				_ = mgr.Close()
			}
			return nil, fmt.Errorf("connecting to host %q (%s): %w", h.Name, h.URI, err)
		}
		hosts[h.Name] = remote
	}
	return NewMultiHostManager(config.LocalHostName, hosts)
}
