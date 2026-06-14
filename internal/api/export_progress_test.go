package api

import (
	"testing"
	"time"
)

// A subscriber receives frames published after it subscribes.
func TestExportProgressBrokerPublishToSubscriber(t *testing.T) {
	b := newExportProgressBroker()
	ch, cancel := b.subscribe("vm-1")
	defer cancel()

	b.publish("vm-1", exportProgressMsg{Name: "img", Percent: 42})

	select {
	case msg := <-ch:
		if msg.Percent != 42 || msg.Name != "img" {
			t.Fatalf("got %+v, want percent=42 name=img", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for progress frame")
	}
}

// A subscriber that connects mid-export immediately receives the last frame.
func TestExportProgressBrokerReplaysLastFrame(t *testing.T) {
	b := newExportProgressBroker()
	b.publish("vm-1", exportProgressMsg{Name: "img", Percent: 73})

	ch, cancel := b.subscribe("vm-1")
	defer cancel()

	select {
	case msg := <-ch:
		if msg.Percent != 73 {
			t.Fatalf("replay got percent=%v, want 73", msg.Percent)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replayed frame")
	}
}

// A terminal Done frame clears the remembered last frame so the next export of
// the same VM starts clean (a fresh subscriber gets nothing until new frames).
func TestExportProgressBrokerDoneClearsLast(t *testing.T) {
	b := newExportProgressBroker()
	b.publish("vm-1", exportProgressMsg{Name: "img", Percent: 50})
	b.publish("vm-1", exportProgressMsg{Name: "img", Done: true})

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
func TestExportProgressBrokerKeyIsolation(t *testing.T) {
	b := newExportProgressBroker()
	ch, cancel := b.subscribe("vm-1")
	defer cancel()

	b.publish("vm-2", exportProgressMsg{Name: "other", Percent: 99})

	select {
	case msg := <-ch:
		t.Fatalf("subscriber on vm-1 received vm-2 frame: %+v", msg)
	case <-time.After(50 * time.Millisecond):
		// correct — isolated
	}
}

// After cancel, the subscriber's channel is closed and removed.
func TestExportProgressBrokerCancelClosesChannel(t *testing.T) {
	b := newExportProgressBroker()
	ch, cancel := b.subscribe("vm-1")
	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("expected channel to be closed after cancel")
	}
}
