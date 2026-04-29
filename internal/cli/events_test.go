package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// withTestEventStore sets storeOverrideForCLI to a real store backed by a temp
// dir and returns the store + cleanup so tests can seed events directly.
func withTestEventStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = filepath.Join(dir, "test.db")
	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	storeOverrideForCLI = func() (*store.Store, func(), error) {
		return s, func() {}, nil
	}
	return s, func() {
		storeOverrideForCLI = nil
		s.Close()
	}
}

func TestCLI_EventsList_Empty(t *testing.T) {
	_, cleanup := withTestEventStore(t)
	defer cleanup()

	out, err := runCLI("events", "list")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	if !strings.Contains(out, "No events.") {
		t.Errorf("expected 'No events.' message, got %q", out)
	}
}

func TestCLI_EventsList_RendersAllFields(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{
		ID:        "evt-1",
		Type:      "vm_started",
		Source:    "libvirt",
		Severity:  "info",
		VMID:      "vm-abc",
		Message:   "VM started",
		CreatedAt: now,
	})

	out, err := runCLI("events", "list")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	for _, want := range []string{"vm_started", "libvirt", "vm-abc", "info"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestCLI_EventsList_FilterByVM(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", VMID: "vm-A", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_started", VMID: "vm-B", CreatedAt: now})

	out, err := runCLI("events", "list", "--vm", "vm-A")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	if !strings.Contains(out, "vm-A") {
		t.Errorf("output missing vm-A\n%s", out)
	}
	if strings.Contains(out, "vm-B") {
		t.Errorf("output should not contain vm-B\n%s", out)
	}
}

func TestCLI_EventsList_FilterBySource(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", Source: "libvirt", VMID: "vm-1", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_created", Source: "app", VMID: "vm-1", CreatedAt: now})

	out, err := runCLI("events", "list", "--source", "libvirt")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	if !strings.Contains(out, "vm_started") || strings.Contains(out, "vm_created") {
		t.Errorf("source filter not applied:\n%s", out)
	}
}

func TestCLI_EventsList_NewestFirst(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	s.PutEvent(&types.Event{ID: "evt-old", Type: "vm_old", VMID: "vm-X", CreatedAt: older})
	s.PutEvent(&types.Event{ID: "evt-new", Type: "vm_new", VMID: "vm-X", CreatedAt: newer})

	out, err := runCLI("events", "list")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	idxNew := strings.Index(out, "vm_new")
	idxOld := strings.Index(out, "vm_old")
	if idxNew < 0 || idxOld < 0 || idxNew >= idxOld {
		t.Errorf("expected newest first; vm_new=%d vm_old=%d\n%s", idxNew, idxOld, out)
	}
}

func TestCLI_EventsList_SinceDuration(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	old := time.Now().Add(-10 * time.Minute)
	recent := time.Now().Add(-1 * time.Minute)
	s.PutEvent(&types.Event{ID: "evt-old", Type: "old_event", CreatedAt: old})
	s.PutEvent(&types.Event{ID: "evt-recent", Type: "recent_event", CreatedAt: recent})

	out, err := runCLI("events", "list", "--since", "5m")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	if !strings.Contains(out, "recent_event") || strings.Contains(out, "old_event") {
		t.Errorf("--since 5m did not exclude old event:\n%s", out)
	}
}

func TestCLI_EventsList_LimitCaps(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	for i := 0; i < 5; i++ {
		s.PutEvent(&types.Event{
			ID:        "evt-" + string(rune('A'+i)),
			Type:      "vm_evt_" + string(rune('A'+i)),
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	out, err := runCLI("events", "list", "--limit", "2")
	if err != nil {
		t.Fatalf("events list --limit 2: %v", err)
	}
	// Header + 2 rows = 3 newline-separated lines (plus trailing).
	dataLines := 0
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(ln, "vm_evt_") {
			dataLines++
		}
	}
	if dataLines != 2 {
		t.Errorf("expected 2 event rows, got %d:\n%s", dataLines, out)
	}
}

func TestCLI_EventsList_InvalidSince(t *testing.T) {
	_, cleanup := withTestEventStore(t)
	defer cleanup()

	_, err := runCLI("events", "list", "--since", "not-a-thing")
	if err == nil {
		t.Fatal("expected error for invalid --since")
	}
	if !strings.Contains(err.Error(), "invalid --since") {
		t.Errorf("wrong error: %v", err)
	}
}
