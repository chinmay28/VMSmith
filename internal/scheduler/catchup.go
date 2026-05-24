package scheduler

import (
	"time"

	"github.com/robfig/cron/v3"
)

// missedFires returns the scheduled fire times strictly after lastTick and at
// or before now, in chronological order. The result is capped at max entries
// (when max > 0); when more than max fires were missed the EARLIEST max are
// returned and the caller is expected to warn about the truncation. A zero or
// future lastTick yields no fires.
func missedFires(sched cron.Schedule, lastTick, now time.Time, max int) []time.Time {
	if sched == nil || lastTick.IsZero() || !lastTick.Before(now) {
		return nil
	}
	var out []time.Time
	t := sched.Next(lastTick)
	for !t.After(now) {
		out = append(out, t)
		if max > 0 && len(out) >= max {
			break
		}
		next := sched.Next(t)
		if !next.After(t) {
			// Defensive: a degenerate schedule that does not advance would
			// otherwise loop forever.
			break
		}
		t = next
	}
	return out
}
