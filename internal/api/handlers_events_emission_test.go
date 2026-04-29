package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// drainEvents pulls every event currently buffered in ch (with a short
// timeout per receive) and returns them in arrival order.
func drainEvents(t *testing.T, ch <-chan *types.Event, want int) []*types.Event {
	t.Helper()
	got := make([]*types.Event, 0, want)
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < want && time.Now().Before(deadline) {
		select {
		case evt := <-ch:
			got = append(got, evt)
		case <-time.After(100 * time.Millisecond):
		}
	}
	return got
}

func wireEventBus(t *testing.T, ts *httptest.Server) (*events.EventBus, <-chan *types.Event, func()) {
	t.Helper()
	// The httptest.Server's handler is the api.Server (which embeds chi.Mux).
	srv, ok := ts.Config.Handler.(*Server)
	if !ok {
		t.Fatalf("test server handler is not *api.Server")
	}
	bus := events.New(memStore{})
	bus.Start()
	srv.SetEventBus(bus)
	ch, cancel := bus.Subscribe("test")
	teardown := func() {
		cancel()
		bus.Stop()
	}
	return bus, ch, teardown
}

// memStore is a tiny in-memory events.Store for tests.
type memStore struct{}

func (memStore) AppendEvent(evt *types.Event) (uint64, error) {
	evt.ID = "1"
	return 1, nil
}

func TestVMLifecycleEmitsAppEvents(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	_, ch, stop := wireEventBus(t, ts)
	defer stop()

	// Create
	body := []byte(`{"name":"vmA","image":"rocky9.qcow2","cpus":1,"ram_mb":512,"disk_gb":10,"ssh_pub_key":"ssh-rsa AAAA test"}`)
	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", resp.StatusCode)
	}

	// Find the seeded VM.
	vms, _ := mockMgr.List(nil)
	if len(vms) != 1 {
		t.Fatalf("want 1 VM after create, got %d", len(vms))
	}
	id := vms[0].ID

	// Start, stop, delete.
	for _, path := range []string{"/start", "/stop"} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/vms/"+id+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/"+id, nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	// Expect 4 app events: created, start_requested, stop_requested, deleted.
	got := drainEvents(t, ch, 4)
	if len(got) < 4 {
		t.Fatalf("want at least 4 events, got %d: %+v", len(got), got)
	}

	wantTypes := []string{"vm.created", "vm.start_requested", "vm.stop_requested", "vm.deleted"}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, got[i].Type, want)
		}
		if got[i].Source != types.EventSourceApp {
			t.Errorf("event[%d].Source = %q, want %q", i, got[i].Source, types.EventSourceApp)
		}
		if got[i].VMID == "" {
			t.Errorf("event[%d].VMID empty", i)
		}
	}
}

func TestSnapshotCRUDEmitsAppEvents(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-test", Name: "vmtest", State: types.VMStateRunning})

	_, ch, stop := wireEventBus(t, ts)
	defer stop()

	// Create snapshot
	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-test/snapshots", "application/json",
		bytes.NewReader([]byte(`{"name":"snap1"}`)))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create snapshot failed: err=%v status=%d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Restore
	resp, err = http.Post(ts.URL+"/api/v1/vms/vm-test/snapshots/snap1/restore", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("restore failed: err=%v status=%d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Delete
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/vm-test/snapshots/snap1", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	got := drainEvents(t, ch, 3)
	if len(got) < 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(got), got)
	}
	for i, want := range []string{"snapshot.created", "snapshot.restored", "snapshot.deleted"} {
		if got[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, got[i].Type, want)
		}
		if got[i].Attributes["snapshot"] != "snap1" {
			t.Errorf("event[%d].Attributes[snapshot] = %q, want snap1", i, got[i].Attributes["snapshot"])
		}
	}
}

func TestPublishAppEvent_NoBus(t *testing.T) {
	// publishAppEvent must be a no-op when no bus is wired.
	s := &Server{}
	s.publishAppEvent("vm.created", "vm-1", "test", nil) // must not panic
}
