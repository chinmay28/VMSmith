package events

import (
	"context"
	"time"

	"github.com/vmsmith/vmsmith/internal/logger"
)

// PruneStore is the persistence interface the retention loop requires.
type PruneStore interface {
	PruneEvents(maxRecords int) (int, error)
	PruneEventsByAge(maxAge time.Duration) (int, error)
}

// Retention runs a periodic loop that deletes old events from the store
// once they exceed maxRecords or are older than maxAge.
type Retention struct {
	store      PruneStore
	maxRecords int
	maxAge     time.Duration
	interval   time.Duration
	bus        *EventBus
}

// NewRetention constructs a retention loop.  bus may be nil, in which case
// pruning still runs but no system events are emitted on each sweep.
//
// maxRecords <= 0 disables count-based pruning.  maxAge <= 0 disables
// age-based pruning.  When both are disabled the loop never starts.
func NewRetention(store PruneStore, maxRecords int, maxAge, interval time.Duration, bus *EventBus) *Retention {
	return &Retention{
		store:      store,
		maxRecords: maxRecords,
		maxAge:     maxAge,
		interval:   interval,
		bus:        bus,
	}
}

// Run blocks until ctx is cancelled, sweeping at the configured interval.
// A non-positive interval, or both maxRecords and maxAge non-positive,
// disables the loop entirely (Run returns immediately).
func (r *Retention) Run(ctx context.Context) {
	if r.interval <= 0 {
		return
	}
	if r.maxRecords <= 0 && r.maxAge <= 0 {
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
	var deletedRecords, deletedAge int

	if r.maxRecords > 0 {
		n, err := r.store.PruneEvents(r.maxRecords)
		if err != nil {
			logger.Warn("events", "retention sweep failed", "phase", "max_records", "error", err.Error())
		} else {
			deletedRecords = n
		}
	}

	if r.maxAge > 0 {
		n, err := r.store.PruneEventsByAge(r.maxAge)
		if err != nil {
			logger.Warn("events", "retention sweep failed", "phase", "max_age", "error", err.Error())
		} else {
			deletedAge = n
		}
	}

	total := deletedRecords + deletedAge
	if total == 0 {
		return
	}
	logger.Info("events", "retention sweep deleted events",
		"deleted", itoa(total),
		"deleted_max_records", itoa(deletedRecords),
		"deleted_max_age", itoa(deletedAge),
		"max_records", itoa(r.maxRecords),
		"max_age_seconds", itoa(int(r.maxAge/time.Second)))
	if r.bus != nil {
		evt := NewSystemEvent("events.retention_pruned", "info",
			"events retention sweep deleted old events")
		evt.Attributes = map[string]string{
			"deleted":             itoa(total),
			"deleted_max_records": itoa(deletedRecords),
			"deleted_max_age":     itoa(deletedAge),
			"max_records":         itoa(r.maxRecords),
			"max_age_seconds":     itoa(int(r.maxAge / time.Second)),
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
