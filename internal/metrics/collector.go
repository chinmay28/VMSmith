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

// prevCounters holds the counter values from the previous sample for a VM.
// Used to compute per-second rates from cumulative libvirt counters.
type prevCounters struct {
	sampleTime    time.Time
	cpuTimeNs     uint64 // cumulative CPU nanoseconds (cpu.time)
	numVcpus      int    // number of active vCPUs at sample time
	diskRdBytes   uint64 // cumulative disk read bytes (sum across devices)
	diskWrBytes   uint64 // cumulative disk write bytes
	netRxBytes    uint64 // cumulative net receive bytes (sum across ifaces)
	netTxBytes    uint64 // cumulative net transmit bytes
	hasCPU        bool
	hasDisk       bool
	hasNet        bool
}

// LibvirtMetricsManager implements Manager using libvirt's bulk stats API.
type LibvirtMetricsManager struct {
	conn     *libvirt.Connect
	interval time.Duration
	histSize int

	mu       sync.RWMutex
	rings    map[string]*ring     // vmName -> ring
	states   map[string]string    // vmName -> state string
	vmNames  map[string]string    // libvirt domain name -> vmsmith VM ID
	prev     map[string]*prevCounters // vmName -> previous counters

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewLibvirtMetricsManager creates a new metrics manager backed by libvirt.
func NewLibvirtMetricsManager(conn *libvirt.Connect, interval time.Duration, histSize int) *LibvirtMetricsManager {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if histSize <= 0 {
		histSize = 360
	}
	return &LibvirtMetricsManager{
		conn:     conn,
		interval: interval,
		histSize: histSize,
		rings:    make(map[string]*ring),
		states:   make(map[string]string),
		vmNames:  make(map[string]string),
		prev:     make(map[string]*prevCounters),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
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
// vmID is the vmsmith VM ID (e.g. "vm-1741234567890123"); it is matched against
// the domain name stored in libvirt (which vmsmith sets to the VM name/ID).
// Returns nil when no samples have been collected yet.
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

// sample calls libvirt's bulk stats API and updates per-VM rings.
func (m *LibvirtMetricsManager) sample() {
	now := time.Now()

	statsSlice, err := m.conn.GetAllDomainStats(
		nil, // nil = all domains
		statsBitmask,
		libvirt.CONNECT_GET_ALL_DOMAINS_STATS_ACTIVE,
	)
	if err != nil {
		logger.Warn("metrics", "GetAllDomainStats failed", "error", err.Error())
		return
	}

	// Collect the set of domain names we saw this round for stale-ring pruning.
	seenDomains := make(map[string]struct{}, len(statsSlice))

	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range statsSlice {
		ds := &statsSlice[i]
		if ds.Domain == nil {
			continue
		}

		domName, err := ds.Domain.GetName()
		if err != nil {
			continue
		}
		seenDomains[domName] = struct{}{}

		// Determine state string.
		stateStr := "unknown"
		if ds.State != nil && ds.State.StateSet {
			stateStr = domainStateString(int(ds.State.State))
		}

		// Ensure ring exists.
		if _, ok := m.rings[domName]; !ok {
			m.rings[domName] = newRing(m.histSize)
		}
		m.states[domName] = stateStr

		// Build metric sample.
		sample := types.MetricSample{Timestamp: now}
		prev := m.prev[domName]

		// --- CPU % ---
		if ds.Cpu != nil && ds.Cpu.TimeSet {
			numVcpus := len(ds.Vcpu)
			if numVcpus == 0 {
				numVcpus = 1
			}
			if prev != nil && prev.hasCPU {
				dtNs := now.Sub(prev.sampleTime).Nanoseconds()
				if dtNs > 0 {
					deltaCPU := int64(ds.Cpu.Time) - int64(prev.cpuTimeNs)
					if deltaCPU >= 0 {
						// cpu% = delta_ns / dt_ns / vcpus * 100
						cpuPct := (float64(deltaCPU) / float64(dtNs) / float64(numVcpus)) * 100.0
						// Clamp to [0, 100]
						if cpuPct < 0 {
							cpuPct = 0
						}
						if cpuPct > 100.0 {
							cpuPct = 100.0
						}
						sample.CPUPercent = &cpuPct
					}
					// negative delta → counter reset → nil (no rate)
				}
			}
			// Update prev counters (always update for next round).
			if prev == nil {
				prev = &prevCounters{}
				m.prev[domName] = prev
			}
			prev.cpuTimeNs = ds.Cpu.Time
			prev.numVcpus = numVcpus
			prev.hasCPU = true
		}

		// --- Memory ---
		if ds.Balloon != nil {
			// MemUsedMB: prefer balloon.rss (RSS from guest kernel), fall back to balloon.current (allocated MB).
			if ds.Balloon.RssSet && ds.Balloon.Rss > 0 {
				mb := ds.Balloon.Rss / 1024 // libvirt reports in KiB
				sample.MemUsedMB = &mb
			} else if ds.Balloon.CurrentSet && ds.Balloon.Current > 0 {
				mb := ds.Balloon.Current / 1024
				sample.MemUsedMB = &mb
			}
			// MemAvailMB: balloon.available - balloon.unused (guest-agent dependent).
			if ds.Balloon.AvailableSet && ds.Balloon.UnusedSet {
				if ds.Balloon.Available >= ds.Balloon.Unused {
					mb := (ds.Balloon.Available - ds.Balloon.Unused) / 1024
					sample.MemAvailMB = &mb
				}
			}
		}

		// --- Disk I/O rates ---
		var totalRdBytes, totalWrBytes uint64
		hasDiskData := false
		for _, blk := range ds.Block {
			if blk.RdBytesSet {
				totalRdBytes += blk.RdBytes
				hasDiskData = true
			}
			if blk.WrBytesSet {
				totalWrBytes += blk.WrBytes
				hasDiskData = true
			}
		}
		if hasDiskData {
			if prev != nil && prev.hasDisk {
				dtSec := now.Sub(prev.sampleTime).Seconds()
				if dtSec > 0 {
					deltaRd := int64(totalRdBytes) - int64(prev.diskRdBytes)
					deltaWr := int64(totalWrBytes) - int64(prev.diskWrBytes)
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
				m.prev[domName] = prev
			}
			prev.diskRdBytes = totalRdBytes
			prev.diskWrBytes = totalWrBytes
			prev.hasDisk = true
		}

		// --- Network I/O rates ---
		var totalRxBytes, totalTxBytes uint64
		hasNetData := false
		for _, iface := range ds.Net {
			if iface.RxBytesSet {
				totalRxBytes += iface.RxBytes
				hasNetData = true
			}
			if iface.TxBytesSet {
				totalTxBytes += iface.TxBytes
				hasNetData = true
			}
		}
		if hasNetData {
			if prev != nil && prev.hasNet {
				dtSec := now.Sub(prev.sampleTime).Seconds()
				if dtSec > 0 {
					deltaRx := int64(totalRxBytes) - int64(prev.netRxBytes)
					deltaTx := int64(totalTxBytes) - int64(prev.netTxBytes)
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
				m.prev[domName] = prev
			}
			prev.netRxBytes = totalRxBytes
			prev.netTxBytes = totalTxBytes
			prev.hasNet = true
		}

		// Stamp sample time after all deltas are computed.
		if prev != nil {
			prev.sampleTime = now
		}

		m.rings[domName].push(sample)
	}

	// Prune rings for VMs that have not been seen for >5 minutes.
	pruneThreshold := 5 * time.Minute
	for domName, r := range m.rings {
		if _, seen := seenDomains[domName]; seen {
			continue
		}
		latest := r.latest()
		if latest == nil || now.Sub(latest.Timestamp) > pruneThreshold {
			delete(m.rings, domName)
			delete(m.states, domName)
			delete(m.prev, domName)
			delete(m.vmNames, domName)
		}
	}
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
