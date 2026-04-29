package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

// statsBitmask selects the libvirt stat groups we care about.
const statsBitmask = libvirt.DOMAIN_STATS_STATE |
	libvirt.DOMAIN_STATS_CPU_TOTAL |
	libvirt.DOMAIN_STATS_BALLOON |
	libvirt.DOMAIN_STATS_VCPU |
	libvirt.DOMAIN_STATS_INTERFACE |
	libvirt.DOMAIN_STATS_BLOCK

// stalePruneInterval is how long a VM may be missing from the sampler
// output before its ring buffer and prev-counters are evicted.
const stalePruneInterval = 5 * time.Minute

// NameToIDFunc translates a libvirt domain name to a vmsmith VM ID.
// Returns ("", false) when the name is unknown.
//
// The metrics sampler indexes its rings by VM ID so that the API/CLI (which
// only know the VM ID) can look samples up without knowing the libvirt domain
// name (which is the user-supplied VM Name).  Callers are expected to back
// this with a small cache; see store.NameToIDFunc for the default impl.
type NameToIDFunc func(name string) (string, bool)

// prevCounters holds the counter values from the previous sample for a VM.
// Used to compute per-second rates from cumulative libvirt counters.
type prevCounters struct {
	sampleTime  time.Time
	cpuTimeNs   uint64 // cumulative CPU nanoseconds (cpu.time)
	numVcpus    int    // number of active vCPUs at sample time
	diskRdBytes uint64 // cumulative disk read bytes (sum across devices)
	diskWrBytes uint64 // cumulative disk write bytes
	netRxBytes  uint64 // cumulative net receive bytes (sum across ifaces)
	netTxBytes  uint64 // cumulative net transmit bytes
	hasCPU      bool
	hasDisk     bool
	hasNet      bool
}

// domainSample is a libvirt-agnostic snapshot of a single domain's stats.
// Used internally to make the per-domain rate math testable without libvirt.
type domainSample struct {
	Name  string
	State string

	HasCPU    bool
	CPUTimeNs uint64
	NumVcpus  int

	HasBalloon       bool
	MemRSSKiB        uint64
	MemCurrentKiB    uint64
	MemAvailableKiB  uint64
	MemUnusedKiB     uint64
	BalloonRssSet    bool
	BalloonCurSet    bool
	BalloonAvailSet  bool
	BalloonUnusedSet bool

	HasDisk     bool
	DiskRdBytes uint64
	DiskWrBytes uint64

	HasNet     bool
	NetRxBytes uint64
	NetTxBytes uint64
}

// domainStatsProvider returns a libvirt-agnostic snapshot of all active
// domain stats.  The libvirt-backed implementation wraps GetAllDomainStats;
// tests can substitute a synthetic provider that feeds counter sequences.
type domainStatsProvider interface {
	GetAllDomainStats() ([]domainSample, error)
}

// LibvirtMetricsManager implements Manager using libvirt's bulk stats API.
type LibvirtMetricsManager struct {
	provider domainStatsProvider
	resolver NameToIDFunc
	interval time.Duration
	histSize int

	mu      sync.RWMutex
	rings   map[string]*ring         // vmID -> ring
	states  map[string]string        // vmID -> state string
	vmNames map[string]string        // libvirt domain name -> vmsmith VM ID
	prev    map[string]*prevCounters // vmID -> previous counters

	stopCh chan struct{}
	doneCh chan struct{}
	now    func() time.Time // overridable for tests
}

// NewLibvirtMetricsManager creates a new metrics manager backed by libvirt.
//
// resolver translates libvirt domain names into vmsmith VM IDs; it may be nil
// (in which case rings are keyed by domain name and the API/CLI lookup will
// fail to find samples for VMs whose ID differs from their name).
func NewLibvirtMetricsManager(conn *libvirt.Connect, resolver NameToIDFunc, interval time.Duration, histSize int) *LibvirtMetricsManager {
	provider := &libvirtProvider{conn: conn}
	return newManagerForProvider(provider, resolver, interval, histSize)
}

// newManagerForProvider is the test-friendly constructor; it accepts any
// domainStatsProvider so unit tests can inject synthetic counter sequences.
func newManagerForProvider(provider domainStatsProvider, resolver NameToIDFunc, interval time.Duration, histSize int) *LibvirtMetricsManager {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if histSize <= 0 {
		histSize = 360
	}
	return &LibvirtMetricsManager{
		provider: provider,
		resolver: resolver,
		interval: interval,
		histSize: histSize,
		rings:    make(map[string]*ring),
		states:   make(map[string]string),
		vmNames:  make(map[string]string),
		prev:     make(map[string]*prevCounters),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		now:      time.Now,
	}
}

// SetResolver updates the name-to-ID resolver after construction.
// Useful when the daemon wires the metrics sampler before the store is fully
// initialised.
func (m *LibvirtMetricsManager) SetResolver(resolver NameToIDFunc) {
	m.mu.Lock()
	m.resolver = resolver
	m.mu.Unlock()
}

// Start launches the background sampling goroutine.
func (m *LibvirtMetricsManager) Start(ctx context.Context) error {
	go m.run(ctx)
	return nil
}

// Stop signals the sampling goroutine to exit and waits for it to finish.
func (m *LibvirtMetricsManager) Stop() error {
	close(m.stopCh)
	<-m.doneCh
	return nil
}

// Snapshot returns the current metrics snapshot for a VM identified by vmID.
// Returns (nil, nil) when no samples have been collected yet.
func (m *LibvirtMetricsManager) Snapshot(vmID string) (*types.VMStatsSnapshot, error) {
	m.mu.RLock()
	r, ok := m.rings[vmID]
	state := m.states[vmID]
	m.mu.RUnlock()

	if !ok {
		return nil, nil
	}

	history := r.all()
	latest := r.latest()

	var lastAt *time.Time
	if latest != nil {
		t := latest.Timestamp
		lastAt = &t
	}

	return &types.VMStatsSnapshot{
		VMID:            vmID,
		State:           state,
		LastSampledAt:   lastAt,
		Current:         latest,
		History:         history,
		IntervalSeconds: int(m.interval.Seconds()),
		HistorySize:     m.histSize,
	}, nil
}

// run is the background sampling loop.
func (m *LibvirtMetricsManager) run(ctx context.Context) {
	defer close(m.doneCh)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	// Do an immediate first sample.
	m.sample()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sample()
		}
	}
}

// sample fetches a fresh round of domain stats and feeds them to processSamples.
func (m *LibvirtMetricsManager) sample() {
	samples, err := m.provider.GetAllDomainStats()
	if err != nil {
		logger.Warn("metrics", "GetAllDomainStats failed", "error", err.Error())
		return
	}
	m.processSamples(samples, m.now())
}

// processSamples updates per-VM rings from a slice of domain samples and
// prunes stale entries.  Pure logic — tested directly with synthetic input.
func (m *LibvirtMetricsManager) processSamples(samples []domainSample, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	resolver := m.resolver
	seen := make(map[string]struct{}, len(samples))

	for i := range samples {
		ds := &samples[i]
		vmID, ok := m.resolveVMID(resolver, ds.Name)
		if !ok {
			// No mapping → skip.  Without a vmID we cannot service /stats
			// queries, and indexing by name would re-introduce the bug
			// reviewers flagged.
			continue
		}
		seen[vmID] = struct{}{}
		m.vmNames[ds.Name] = vmID
		m.recordSample(vmID, ds, now)
	}

	m.prune(seen, now)
}

// resolveVMID returns the vmID for a libvirt domain name.  It first consults
// the cache (vmNames), then falls back to the resolver, and finally falls
// back to assuming the domain name *is* the VM ID (back-compat — some unit
// tests seed the manager directly without going through libvirt).
func (m *LibvirtMetricsManager) resolveVMID(resolver NameToIDFunc, name string) (string, bool) {
	if id, ok := m.vmNames[name]; ok {
		return id, true
	}
	if resolver != nil {
		if id, ok := resolver(name); ok {
			return id, true
		}
		return "", false
	}
	// No resolver wired — last resort, key by name.  This matches the legacy
	// behaviour for the (uncommon) case where the daemon is started without
	// a store-backed resolver.
	return name, true
}

// recordSample applies one domain sample to the per-VM ring/state/prev maps.
// Caller must hold m.mu.
func (m *LibvirtMetricsManager) recordSample(vmID string, ds *domainSample, now time.Time) {
	state := ds.State
	if state == "" {
		state = "unknown"
	}

	if _, ok := m.rings[vmID]; !ok {
		m.rings[vmID] = newRing(m.histSize)
	}
	m.states[vmID] = state

	prev := m.prev[vmID]
	sample := types.MetricSample{Timestamp: now}

	// --- CPU % ---
	if ds.HasCPU {
		numVcpus := ds.NumVcpus
		if numVcpus <= 0 {
			numVcpus = 1
		}
		if prev != nil && prev.hasCPU {
			dtNs := now.Sub(prev.sampleTime).Nanoseconds()
			if dtNs > 0 {
				deltaCPU := int64(ds.CPUTimeNs) - int64(prev.cpuTimeNs)
				if deltaCPU >= 0 {
					cpuPct := (float64(deltaCPU) / float64(dtNs) / float64(numVcpus)) * 100.0
					if cpuPct < 0 {
						cpuPct = 0
					}
					if cpuPct > 100.0 {
						cpuPct = 100.0
					}
					sample.CPUPercent = &cpuPct
				}
				// negative delta → counter reset → leave nil
			}
		}
		if prev == nil {
			prev = &prevCounters{}
			m.prev[vmID] = prev
		}
		prev.cpuTimeNs = ds.CPUTimeNs
		prev.numVcpus = numVcpus
		prev.hasCPU = true
	}

	// --- Memory ---
	if ds.HasBalloon {
		if ds.BalloonRssSet && ds.MemRSSKiB > 0 {
			mb := ds.MemRSSKiB / 1024
			sample.MemUsedMB = &mb
		} else if ds.BalloonCurSet && ds.MemCurrentKiB > 0 {
			mb := ds.MemCurrentKiB / 1024
			sample.MemUsedMB = &mb
		}
		if ds.BalloonAvailSet && ds.BalloonUnusedSet {
			if ds.MemAvailableKiB >= ds.MemUnusedKiB {
				mb := (ds.MemAvailableKiB - ds.MemUnusedKiB) / 1024
				sample.MemAvailMB = &mb
			}
		}
	}

	// --- Disk I/O rates ---
	if ds.HasDisk {
		if prev != nil && prev.hasDisk {
			dtSec := now.Sub(prev.sampleTime).Seconds()
			if dtSec > 0 {
				deltaRd := int64(ds.DiskRdBytes) - int64(prev.diskRdBytes)
				deltaWr := int64(ds.DiskWrBytes) - int64(prev.diskWrBytes)
				if deltaRd >= 0 {
					bps := uint64(float64(deltaRd) / dtSec)
					sample.DiskReadBps = &bps
				}
				if deltaWr >= 0 {
					bps := uint64(float64(deltaWr) / dtSec)
					sample.DiskWriteBps = &bps
				}
			}
		}
		if prev == nil {
			prev = &prevCounters{}
			m.prev[vmID] = prev
		}
		prev.diskRdBytes = ds.DiskRdBytes
		prev.diskWrBytes = ds.DiskWrBytes
		prev.hasDisk = true
	}

	// --- Network I/O rates ---
	if ds.HasNet {
		if prev != nil && prev.hasNet {
			dtSec := now.Sub(prev.sampleTime).Seconds()
			if dtSec > 0 {
				deltaRx := int64(ds.NetRxBytes) - int64(prev.netRxBytes)
				deltaTx := int64(ds.NetTxBytes) - int64(prev.netTxBytes)
				if deltaRx >= 0 {
					bps := uint64(float64(deltaRx) / dtSec)
					sample.NetRxBps = &bps
				}
				if deltaTx >= 0 {
					bps := uint64(float64(deltaTx) / dtSec)
					sample.NetTxBps = &bps
				}
			}
		}
		if prev == nil {
			prev = &prevCounters{}
			m.prev[vmID] = prev
		}
		prev.netRxBytes = ds.NetRxBytes
		prev.netTxBytes = ds.NetTxBytes
		prev.hasNet = true
	}

	// Stamp sample time after all deltas are computed.
	if prev != nil {
		prev.sampleTime = now
	}

	m.rings[vmID].push(sample)
}

// prune evicts rings/state/prev for VMs not seen in the last sample round
// when their newest sample is older than stalePruneInterval.
// Caller must hold m.mu.
func (m *LibvirtMetricsManager) prune(seen map[string]struct{}, now time.Time) {
	for vmID, r := range m.rings {
		if _, ok := seen[vmID]; ok {
			continue
		}
		latest := r.latest()
		if latest == nil || now.Sub(latest.Timestamp) > stalePruneInterval {
			delete(m.rings, vmID)
			delete(m.states, vmID)
			delete(m.prev, vmID)
		}
	}
	// Also prune the name → ID cache for any name whose vmID has been evicted.
	for name, vmID := range m.vmNames {
		if _, ok := m.rings[vmID]; !ok {
			delete(m.vmNames, name)
		}
	}
}

// libvirtProvider wraps a *libvirt.Connect into a domainStatsProvider.
type libvirtProvider struct {
	conn *libvirt.Connect
}

func (p *libvirtProvider) GetAllDomainStats() ([]domainSample, error) {
	statsSlice, err := p.conn.GetAllDomainStats(
		nil,
		statsBitmask,
		libvirt.CONNECT_GET_ALL_DOMAINS_STATS_ACTIVE,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := range statsSlice {
			if statsSlice[i].Domain != nil {
				statsSlice[i].Domain.Free()
			}
		}
	}()

	out := make([]domainSample, 0, len(statsSlice))
	for i := range statsSlice {
		ds := &statsSlice[i]
		if ds.Domain == nil {
			continue
		}
		name, err := ds.Domain.GetName()
		if err != nil {
			continue
		}
		s := domainSample{Name: name}

		if ds.State != nil && ds.State.StateSet {
			s.State = domainStateString(int(ds.State.State))
		} else {
			s.State = "unknown"
		}

		if ds.Cpu != nil && ds.Cpu.TimeSet {
			s.HasCPU = true
			s.CPUTimeNs = ds.Cpu.Time
			s.NumVcpus = len(ds.Vcpu)
			if s.NumVcpus == 0 {
				s.NumVcpus = 1
			}
		}

		if ds.Balloon != nil {
			s.HasBalloon = true
			s.BalloonRssSet = ds.Balloon.RssSet
			s.MemRSSKiB = ds.Balloon.Rss
			s.BalloonCurSet = ds.Balloon.CurrentSet
			s.MemCurrentKiB = ds.Balloon.Current
			s.BalloonAvailSet = ds.Balloon.AvailableSet
			s.MemAvailableKiB = ds.Balloon.Available
			s.BalloonUnusedSet = ds.Balloon.UnusedSet
			s.MemUnusedKiB = ds.Balloon.Unused
		}

		var totalRd, totalWr uint64
		hasDisk := false
		for _, blk := range ds.Block {
			if blk.RdBytesSet {
				totalRd += blk.RdBytes
				hasDisk = true
			}
			if blk.WrBytesSet {
				totalWr += blk.WrBytes
				hasDisk = true
			}
		}
		if hasDisk {
			s.HasDisk = true
			s.DiskRdBytes = totalRd
			s.DiskWrBytes = totalWr
		}

		var totalRx, totalTx uint64
		hasNet := false
		for _, iface := range ds.Net {
			if iface.RxBytesSet {
				totalRx += iface.RxBytes
				hasNet = true
			}
			if iface.TxBytesSet {
				totalTx += iface.TxBytes
				hasNet = true
			}
		}
		if hasNet {
			s.HasNet = true
			s.NetRxBytes = totalRx
			s.NetTxBytes = totalTx
		}

		out = append(out, s)
	}
	return out, nil
}

// domainStateString converts a libvirt domain state integer to a readable string.
func domainStateString(state int) string {
	switch libvirt.DomainState(state) {
	case libvirt.DOMAIN_RUNNING:
		return "running"
	case libvirt.DOMAIN_BLOCKED:
		return "blocked"
	case libvirt.DOMAIN_PAUSED:
		return "paused"
	case libvirt.DOMAIN_SHUTDOWN:
		return "shutdown"
	case libvirt.DOMAIN_SHUTOFF:
		return "stopped"
	case libvirt.DOMAIN_CRASHED:
		return "crashed"
	case libvirt.DOMAIN_PMSUSPENDED:
		return "suspended"
	default:
		return "unknown"
	}
}
