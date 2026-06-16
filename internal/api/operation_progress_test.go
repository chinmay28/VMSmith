package api

import (
	"testing"
	"time"
)

// A subscriber receives frames published after it subscribes.
func TestOperationProgressBrokerPublishToSubscriber(t *testing.T) {
	b := newOperationProgressBroker()
	ch, cancel := b.subscribe("vm-1")
	defer cancel()

	b.publish("vm-1", operationProgressMsg{Op: "export", Name: "img", Percent: 42})

	select {
	case msg := <-ch:
		if msg.Percent != 42 || msg.Name != "img" || msg.Op != "export" {
			t.Fatalf("got %+v, want percent=42 name=img op=export", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for progress frame")
	}
}

// A subscriber that connects mid-operation immediately receives the last frame.
func TestOperationProgressBrokerReplaysLastFrame(t *testing.T) {
	b := newOperationProgressBroker()
	b.publish("vm-1", operationProgressMsg{Op: "clone", Name: "img", Percent: 73})

	ch, cancel := b.subscribe("vm-1")
	defer cancel()

	select {
	case msg := <-ch:
		if msg.Percent != 73 || msg.Op != "clone" {
			t.Fatalf("replay got %+v, want percent=73 op=clone", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replayed frame")
	}
}

// A terminal Done frame clears the remembered last frame so the next operation
// on the same VM starts clean (a fresh subscriber gets nothing until new frames).
func TestOperationProgressBrokerDoneClearsLast(t *testing.T) {
	b := newOperationProgressBroker()
	b.publish("vm-1", operationProgressMsg{Op: "export", Name: "img", Percent: 50})
	b.publish("vm-1", operationProgressMsg{Op: "export", Name: "img", Done: true})

	ch, cancel := b.subscribe("vm-1")
	defer cancel()

	select {
	case msg := <-ch:
		t.Fatalf("expected no replayed frame after Done, got %+v", msg)
	case <-time.After(50 * time.Millisecond):
		// no frame — correct
	}
}

// Frames are scoped per VM key: a subscriber on one VM never sees another's.
func TestOperationProgressBrokerKeyIsolation(t *testing.T) {
	b := newOperationProgressBroker()
	ch, cancel := b.subscribe("vm-1")
	defer cancel()

	b.publish("vm-2", operationProgressMsg{Op: "export", Name: "other", Percent: 99})

	select {
	case msg := <-ch:
		t.Fatalf("subscriber on vm-1 received vm-2 frame: %+v", msg)
	case <-time.After(50 * time.Millisecond):
		// correct — isolated
	}
}

// After cancel, the subscriber's channel is closed and removed.
func TestOperationProgressBrokerCancelClosesChannel(t *testing.T) {
	b := newOperationProgressBroker()
	ch, cancel := b.subscribe("vm-1")
	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("expected channel to be closed after cancel")
	}
}

// progressCallback throttles to whole-percent advances and always lets 100 through.
func TestOperationProgressBrokerThrottlesCallback(t *testing.T) {
	b := newOperationProgressBroker()
	ch, cancel := b.subscribe("vm-1")
	defer cancel()

	cb := b.progressCallback("vm-1", "export", "img")
	cb(0.2) // first call: 0.2 - (-1) >= 1 → published
	cb(0.5) // 0.5 - 0.2 < 1 → dropped
	cb(1.5) // 1.5 - 0.2 >= 1 → published

	got := drainPercents(ch, 2)
	if len(got) != 2 || got[0] != 0.2 || got[1] != 1.5 {
		t.Fatalf("throttled percents = %v, want [0.2 1.5]", got)
	}
}

// ReadinessReporter publishes VM readiness frames onto the per-VM channel so a
// subscriber sees the boot progress streamed by the lifecycle monitor.
func TestReadinessReporterPublishesBootFrames(t *testing.T) {
	s := &Server{operationProgress: newOperationProgressBroker()}
	report := s.ReadinessReporter()
	if report == nil {
		t.Fatal("expected a non-nil reporter when broker is configured")
	}

	ch, cancel := s.operationProgress.subscribe("vm-1")
	defer cancel()

	report("vm-1", "boot", 100, true)

	select {
	case msg := <-ch:
		if msg.Op != "boot" || msg.Percent != 100 || !msg.Done {
			t.Fatalf("got %+v, want op=boot percent=100 done=true", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for readiness frame")
	}
}

// With no broker configured the reporter is nil so the manager skips reporting.
func TestReadinessReporterNilWithoutBroker(t *testing.T) {
	s := &Server{}
	if s.ReadinessReporter() != nil {
		t.Fatal("expected nil reporter when broker is unavailable")
	}
}

func drainPercents(ch <-chan operationProgressMsg, n int) []float64 {
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		select {
		case msg := <-ch:
			out = append(out, msg.Percent)
		case <-time.After(time.Second):
			return out
		}
	}
	return out
}
