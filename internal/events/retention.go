package events

import (
	"context"
	"time"

	"github.com/vmsmith/vmsmith/internal/logger"
)

// PruneStore is the persistence interface the retention loop requires.
type PruneStore interface {
	PruneEvents(maxRecords int) (int, error)
}

// Retention runs a periodic loop that deletes old events from the store
// once they exceed maxRecords.
type Retention struct {
	store      PruneStore
	maxRecords int
	interval   time.Duration
	bus        *EventBus
}

// NewRetention constructs a retention loop.  bus may be nil, in which case
// pruning still runs but no system events are emitted on each sweep.
func NewRetention(store PruneStore, maxRecords int, interval time.Duration, bus *EventBus) *Retention {
	return &Retention{
		store:      store,
		maxRecords: maxRecords,
		interval:   interval,
		bus:        bus,
	}
}

// Run blocks until ctx is cancelled, sweeping at the configured interval.
// A maxRecords or interval <= 0 disables the loop entirely (Run returns immediately).
func (r *Retention) Run(ctx context.Context) {
	if r.maxRecords <= 0 || r.interval <= 0 {
		return
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Run once at startup so a daemon restart immediately catches up on retention.
	r.sweep()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *Retention) sweep() {
	deleted, err := r.store.PruneEvents(r.maxRecords)
	if err != nil {
		logger.Warn("events", "retention sweep failed", "error", err.Error())
		return
	}
	if deleted == 0 {
		return
	}
	logger.Info("events", "retention sweep deleted events", "deleted", itoa(deleted), "max_records", itoa(r.maxRecords))
	if r.bus != nil {
		evt := NewSystemEvent("events.retention_pruned", "info",
			"events retention sweep deleted old events")
		evt.Attributes = map[string]string{
			"deleted":     itoa(deleted),
			"max_records": itoa(r.maxRecords),
		}
		r.bus.Publish(evt)
	}
}

func itoa(n int) string {
	// Tiny helper to avoid importing strconv just for this.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
