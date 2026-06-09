package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
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

// ============================================================
// `events list --actor` (4.2.23)
// ============================================================

func TestCLI_EventsList_FilterByActor_ExactMatch(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", Actor: "ops-alice", VMID: "vm-A", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_stopped", Actor: "ops-bob", VMID: "vm-A", CreatedAt: now.Add(time.Second)})
	s.PutEvent(&types.Event{ID: "evt-3", Type: "vm_deleted", Actor: "ops-alice", VMID: "vm-A", CreatedAt: now.Add(2 * time.Second)})

	out, err := runCLI("events", "list", "--actor", "ops-alice")
	if err != nil {
		t.Fatalf("events list --actor: %v", err)
	}
	if !strings.Contains(out, "vm_started") || !strings.Contains(out, "vm_deleted") {
		t.Errorf("--actor ops-alice missed expected rows:\n%s", out)
	}
	if strings.Contains(out, "vm_stopped") {
		t.Errorf("--actor ops-alice should not match ops-bob's vm_stopped:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByActor_CaseSensitive(t *testing.T) {
	// CLI mirrors the API's case-sensitive contract; case-insensitive
	// matching belongs to --search.
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", Actor: "ops-alice", VMID: "vm-A", CreatedAt: now})

	out, err := runCLI("events", "list", "--actor", "Ops-Alice")
	if err != nil {
		t.Fatalf("events list --actor Ops-Alice: %v", err)
	}
	if strings.Contains(out, "vm_started") {
		t.Errorf("--actor Ops-Alice should not match stored ops-alice (case-sensitive):\n%s", out)
	}
	if !strings.Contains(out, "No events") {
		t.Errorf("expected 'No events' message for non-matching actor:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByActor_TrimmedAndEmpty(t *testing.T) {
	// Whitespace-trimmed; empty after trim is a no-op (renders everything).
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", Actor: "ops-alice", VMID: "vm-A", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_stopped", Actor: "ops-bob", VMID: "vm-A", CreatedAt: now.Add(time.Second)})

	// "  ops-alice  " → trimmed → matches.
	out, err := runCLI("events", "list", "--actor", "  ops-alice  ")
	if err != nil {
		t.Fatalf("events list --actor=<padded>: %v", err)
	}
	if !strings.Contains(out, "vm_started") || strings.Contains(out, "vm_stopped") {
		t.Errorf("whitespace-padded actor did not match exactly the alice row:\n%s", out)
	}

	// "   " → empty after trim → no filter applied.
	out, err = runCLI("events", "list", "--actor", "   ")
	if err != nil {
		t.Fatalf("events list --actor=<spaces>: %v", err)
	}
	if !strings.Contains(out, "vm_started") || !strings.Contains(out, "vm_stopped") {
		t.Errorf("blank --actor should be no-op:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByActor_ComposesWithSource(t *testing.T) {
	// --actor narrows further when stacked with --source; the AND semantics
	// must mirror the API.
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", Source: "app", Actor: "ops-alice", VMID: "vm-A", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_pruned", Source: "system", Actor: "ops-alice", VMID: "vm-A", CreatedAt: now.Add(time.Second)})
	s.PutEvent(&types.Event{ID: "evt-3", Type: "vm_stopped", Source: "app", Actor: "ops-bob", VMID: "vm-A", CreatedAt: now.Add(2 * time.Second)})

	out, err := runCLI("events", "list", "--actor", "ops-alice", "--source", "app")
	if err != nil {
		t.Fatalf("events list --actor --source: %v", err)
	}
	if !strings.Contains(out, "vm_started") {
		t.Errorf("expected vm_started in narrowed output:\n%s", out)
	}
	if strings.Contains(out, "vm_pruned") || strings.Contains(out, "vm_stopped") {
		t.Errorf("--actor ops-alice + --source app should not match other rows:\n%s", out)
	}
}

// ============================================================
// `events list --min-severity` (5.4.41)
// ============================================================

func seedMinSeverityCLIEvents(t *testing.T, s *store.Store) {
	t.Helper()
	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_created", Source: "app", Severity: "info", VMID: "vm-1", Message: "info row", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_stopped", Source: "libvirt", Severity: "warn", VMID: "vm-1", Message: "warn row", CreatedAt: now.Add(time.Second)})
	s.PutEvent(&types.Event{ID: "evt-3", Type: "dhcp_exhausted", Source: "system", Severity: "error", Message: "error row", CreatedAt: now.Add(2 * time.Second)})
}

func TestCLI_EventsList_FilterByMinSeverity_Floor(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedMinSeverityCLIEvents(t, s)

	out, err := runCLI("events", "list", "--min-severity", "warn")
	if err != nil {
		t.Fatalf("events list --min-severity warn: %v", err)
	}
	if !strings.Contains(out, "warn row") || !strings.Contains(out, "error row") {
		t.Errorf("warn floor should keep warn + error rows:\n%s", out)
	}
	if strings.Contains(out, "info row") {
		t.Errorf("warn floor should drop the info row:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByMinSeverity_CaseInsensitive(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedMinSeverityCLIEvents(t, s)

	out, err := runCLI("events", "list", "--min-severity", "ERROR")
	if err != nil {
		t.Fatalf("events list --min-severity ERROR: %v", err)
	}
	if !strings.Contains(out, "error row") {
		t.Errorf("error floor should keep the error row:\n%s", out)
	}
	if strings.Contains(out, "warn row") || strings.Contains(out, "info row") {
		t.Errorf("error floor should drop warn + info rows:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByMinSeverity_Invalid(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedMinSeverityCLIEvents(t, s)

	_, err := runCLI("events", "list", "--min-severity", "critical")
	if err == nil {
		t.Fatal("expected error for invalid --min-severity, got nil")
	}
	if !strings.Contains(err.Error(), "min-severity") {
		t.Errorf("error should mention min-severity, got: %v", err)
	}
}

func TestCLI_EventsList_FilterByMinSeverity_ComposesWithSource(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedMinSeverityCLIEvents(t, s)

	// warn floor + libvirt source → only the libvirt warn row.
	out, err := runCLI("events", "list", "--min-severity", "warn", "--source", "libvirt")
	if err != nil {
		t.Fatalf("events list --min-severity --source: %v", err)
	}
	if !strings.Contains(out, "warn row") {
		t.Errorf("expected warn row:\n%s", out)
	}
	if strings.Contains(out, "error row") || strings.Contains(out, "info row") {
		t.Errorf("warn floor + libvirt should drop the system error and info rows:\n%s", out)
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

func TestCLI_EventsList_FilterBySearch_MatchesMessage(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", VMID: "vm-A", Message: "started vm web-prod-01", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_stopped", VMID: "vm-B", Message: "stopped vm db-staging", CreatedAt: now})

	out, err := runCLI("events", "list", "--search", "web-prod")
	if err != nil {
		t.Fatalf("events list --search: %v", err)
	}
	if !strings.Contains(out, "vm_started") || strings.Contains(out, "vm_stopped") {
		t.Errorf("--search did not narrow output:\n%s", out)
	}
}

func TestCLI_EventsList_FilterBySearch_MatchesAttributeValue(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{
		ID:         "evt-1",
		Type:       "port_forward_added",
		Attributes: map[string]string{"host_port": "22001"},
		CreatedAt:  now,
	})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_started", CreatedAt: now})

	out, err := runCLI("events", "list", "--search", "22001")
	if err != nil {
		t.Fatalf("events list --search: %v", err)
	}
	if !strings.Contains(out, "port_forward_added") || strings.Contains(out, "vm_started") {
		t.Errorf("attribute-value search did not narrow output:\n%s", out)
	}
}

func TestCLI_EventsList_FilterBySearch_IsCaseInsensitive(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "dhcp_exhausted", Message: "DHCP pool exhausted", CreatedAt: now})

	out, err := runCLI("events", "list", "--search", "DHCP")
	if err != nil {
		t.Fatalf("events list --search: %v", err)
	}
	if !strings.Contains(out, "dhcp_exhausted") {
		t.Errorf("expected DHCP match via case-insensitive search:\n%s", out)
	}
}

func TestCLI_EventsList_FilterBySearch_NoMatch(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", Message: "ok", CreatedAt: now})

	out, err := runCLI("events", "list", "--search", "needle-not-present")
	if err != nil {
		t.Fatalf("events list --search: %v", err)
	}
	if !strings.Contains(out, "No events.") {
		t.Errorf("expected 'No events.' for no-match search:\n%s", out)
	}
}

func TestCLI_EventsList_FilterBySearch_CombinesWithSource(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "snapshot_created", Source: "app", Message: "snapshot before-deploy", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_stopped", Source: "libvirt", Message: "stopped snapshot host", CreatedAt: now})

	out, err := runCLI("events", "list", "--search", "snapshot", "--source", "app")
	if err != nil {
		t.Fatalf("events list --search --source: %v", err)
	}
	if !strings.Contains(out, "snapshot_created") || strings.Contains(out, "vm_stopped") {
		t.Errorf("--search did not compose with --source:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByResourceID_ExactMatch(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "snapshot_created", ResourceID: "snap-prod-pre", Message: "made", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "snapshot_deleted", ResourceID: "snap-prod-pre", Message: "dropped", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-3", Type: "image_uploaded", ResourceID: "img-other", Message: "uploaded", CreatedAt: now})

	out, err := runCLI("events", "list", "--resource-id", "snap-prod-pre")
	if err != nil {
		t.Fatalf("events list --resource-id: %v", err)
	}
	if !strings.Contains(out, "snapshot_created") || !strings.Contains(out, "snapshot_deleted") {
		t.Errorf("expected both snap-prod-pre events:\n%s", out)
	}
	if strings.Contains(out, "image_uploaded") {
		t.Errorf("--resource-id leaked unrelated event:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByResourceID_CaseSensitive(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "snapshot_created", ResourceID: "snap-prod-pre", Message: "made", CreatedAt: now})

	out, err := runCLI("events", "list", "--resource-id", "SNAP-prod-pre")
	if err != nil {
		t.Fatalf("events list --resource-id: %v", err)
	}
	if !strings.Contains(out, "No events.") {
		t.Errorf("expected 'No events.' for case-mismatched --resource-id:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByResourceID_TrimmedAndEmpty(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "snapshot_created", ResourceID: "snap-prod-pre", Message: "made", CreatedAt: now})

	// Whitespace-only is treated as empty (no filter) — every event is returned.
	out, err := runCLI("events", "list", "--resource-id", "   ")
	if err != nil {
		t.Fatalf("events list --resource-id (blank): %v", err)
	}
	if !strings.Contains(out, "snapshot_created") {
		t.Errorf("expected whitespace --resource-id to disable filter:\n%s", out)
	}

	// Trim semantics on a non-blank target.
	out, err = runCLI("events", "list", "--resource-id", "  snap-prod-pre  ")
	if err != nil {
		t.Fatalf("events list --resource-id (padded): %v", err)
	}
	if !strings.Contains(out, "snapshot_created") {
		t.Errorf("expected trimmed --resource-id to match:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByResourceID_ComposesWithSource(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "snapshot_created", Source: "app", ResourceID: "snap-prod-pre", Message: "made", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "snapshot_lifecycle", Source: "libvirt", ResourceID: "snap-prod-pre", Message: "host event", CreatedAt: now})

	out, err := runCLI("events", "list", "--resource-id", "snap-prod-pre", "--source", "app")
	if err != nil {
		t.Fatalf("events list --resource-id --source: %v", err)
	}
	if !strings.Contains(out, "snapshot_created") || strings.Contains(out, "snapshot_lifecycle") {
		t.Errorf("--resource-id did not compose with --source:\n%s", out)
	}
}

// seedTypePrefixCLIEvents puts a mix of snapshot.*, vm.*, and webhook.* events
// into the local CLI store so --type-prefix tests can assert on each family
// independently.
func seedTypePrefixCLIEvents(t *testing.T, s *store.Store) {
	t.Helper()
	now := time.Now()
	seeds := []*types.Event{
		{ID: "evt-1", Type: "snapshot.created", VMID: "vm-1", CreatedAt: now.Add(-50 * time.Minute)},
		{ID: "evt-2", Type: "snapshot.deleted", VMID: "vm-1", CreatedAt: now.Add(-40 * time.Minute)},
		{ID: "evt-3", Type: "vm.started", VMID: "vm-1", Source: "libvirt", CreatedAt: now.Add(-30 * time.Minute)},
		{ID: "evt-4", Type: "vm.stopped", VMID: "vm-1", Source: "libvirt", CreatedAt: now.Add(-20 * time.Minute)},
		{ID: "evt-5", Type: "webhook.delivery_failed", Source: "system", CreatedAt: now.Add(-10 * time.Minute)},
	}
	for _, evt := range seeds {
		if err := s.PutEvent(evt); err != nil {
			t.Fatalf("PutEvent %s: %v", evt.Type, err)
		}
	}
}

func TestCLI_EventsList_FilterByTypePrefix_MatchesFamily(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedTypePrefixCLIEvents(t, s)

	out, err := runCLI("events", "list", "--type-prefix", "snapshot.")
	if err != nil {
		t.Fatalf("events list --type-prefix: %v", err)
	}
	if !strings.Contains(out, "snapshot.created") || !strings.Contains(out, "snapshot.deleted") {
		t.Errorf("missing snapshot.* events:\n%s", out)
	}
	if strings.Contains(out, "vm.started") || strings.Contains(out, "webhook.") {
		t.Errorf("non-snapshot.* events leaked through prefix filter:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByTypePrefix_CaseInsensitive(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedTypePrefixCLIEvents(t, s)

	out, err := runCLI("events", "list", "--type-prefix", "WEBHOOK.")
	if err != nil {
		t.Fatalf("events list --type-prefix WEBHOOK.: %v", err)
	}
	if !strings.Contains(out, "webhook.delivery_failed") {
		t.Errorf("expected case-insensitive match on uppercase prefix:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByTypePrefix_EmptyOmitsFilter(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedTypePrefixCLIEvents(t, s)

	out, err := runCLI("events", "list", "--type-prefix", "")
	if err != nil {
		t.Fatalf("events list --type-prefix '': %v", err)
	}
	for _, want := range []string{"snapshot.created", "vm.started", "webhook.delivery_failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q present when --type-prefix is empty:\n%s", want, out)
		}
	}
}

func TestCLI_EventsList_FilterByTypePrefix_NoMatchPrintsEmpty(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedTypePrefixCLIEvents(t, s)

	out, err := runCLI("events", "list", "--type-prefix", "schedule.")
	if err != nil {
		t.Fatalf("events list --type-prefix schedule.: %v", err)
	}
	if !strings.Contains(out, "No events.") {
		t.Errorf("expected 'No events.' when prefix has no hits:\n%s", out)
	}
}

func TestCLI_EventsList_FilterByTypePrefix_ComposesWithSource(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedTypePrefixCLIEvents(t, s)

	out, err := runCLI("events", "list", "--type-prefix", "vm.", "--source", "app")
	if err != nil {
		t.Fatalf("events list --type-prefix --source: %v", err)
	}
	if !strings.Contains(out, "No events.") {
		t.Errorf("expected 'No events.' when vm.* + source=app yields nothing:\n%s", out)
	}
}

// --- events follow tests ---

// sseEventTestServer serves a deterministic SSE stream for testing.
type sseEventTestServer struct {
	mu          sync.Mutex
	events      []*types.Event   // events to send on first connect
	replay      []*types.Event   // events to send when Last-Event-ID is set
	authHeaders []string         // captured Authorization headers
	lastIDSeen  []string         // captured Last-Event-ID headers
	queries     []url.Values     // captured request query strings
	statusOnce  int              // optional one-shot status code (0 = 200)
	server      *httptest.Server // backing test server
}

func newSSETestServer(t *testing.T, events []*types.Event) *sseEventTestServer {
	t.Helper()
	srv := &sseEventTestServer{events: events}
	srv.server = httptest.NewServer(http.HandlerFunc(srv.handle))
	t.Cleanup(srv.server.Close)
	return srv
}

func (s *sseEventTestServer) URL() string { return s.server.URL }

func (s *sseEventTestServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.authHeaders = append(s.authHeaders, r.Header.Get("Authorization"))
	s.lastIDSeen = append(s.lastIDSeen, r.Header.Get("Last-Event-ID"))
	s.queries = append(s.queries, r.URL.Query())
	statusOnce := s.statusOnce
	s.statusOnce = 0
	events := s.events
	if r.Header.Get("Last-Event-ID") != "" && s.replay != nil {
		events = s.replay
	}
	s.mu.Unlock()

	if statusOnce != 0 {
		w.WriteHeader(statusOnce)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flush", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for _, e := range events {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", e.ID, e.Type, data)
		flusher.Flush()
	}
	// keep open briefly so the client doesn't immediately reconnect
	select {
	case <-r.Context().Done():
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFollow_PrintsEventsAsTheyArrive(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", Source: "libvirt", Severity: "info", VMID: "vm-A", Message: "started", OccurredAt: time.Now()},
		{ID: "2", Type: "vm.stopped", Source: "libvirt", Severity: "info", VMID: "vm-A", Message: "stopped", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "", eventFilter{}, eventRowOptions{}, &buf); err != nil {
		t.Fatalf("followEventsStream: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"vm.started", "vm.stopped", "vm-A"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFollow_FiltersByVMID(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "2", Type: "vm.started", VMID: "vm-B", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "", eventFilter{vmID: "vm-A"}, eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "vm-A") {
		t.Errorf("vm-A row missing:\n%s", out)
	}
	if strings.Contains(out, "vm-B") {
		t.Errorf("vm-B row should be filtered out:\n%s", out)
	}
}

func TestFollow_FiltersByTypeSourceSeverity(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", Source: "libvirt", Severity: "info", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "2", Type: "vm.created", Source: "app", Severity: "info", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "3", Type: "vm.crashed", Source: "libvirt", Severity: "error", VMID: "vm-A", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "",
		eventFilter{source: "libvirt", severity: "error"}, eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "vm.crashed") {
		t.Errorf("vm.crashed should pass libvirt+error filter:\n%s", out)
	}
	if strings.Contains(out, "vm.started") || strings.Contains(out, "vm.created") {
		t.Errorf("non-matching events should be filtered:\n%s", out)
	}
}

func TestFollow_FiltersByTypePrefix(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "snapshot.created", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "2", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "3", Type: "snapshot.restored", VMID: "vm-A", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "",
		eventFilter{typePrefix: "snapshot."}, eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "snapshot.created") || !strings.Contains(out, "snapshot.restored") {
		t.Errorf("snapshot.* rows should pass type-prefix filter:\n%s", out)
	}
	if strings.Contains(out, "vm.started") {
		t.Errorf("non-snapshot.* events should be filtered:\n%s", out)
	}
}

func TestFollow_FiltersByTypePrefix_CaseInsensitive(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "Snapshot.Created", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "2", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	// Caller-lowercases contract: the filter value is already lowercased
	// upstream of matchesEventFilter. Mixed-case event types still match.
	if err := followEventsStream(ctx, srv.URL(), "",
		eventFilter{typePrefix: "snapshot."}, eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Snapshot.Created") {
		t.Errorf("Snapshot.Created should match snapshot. prefix case-insensitively:\n%s", out)
	}
	if strings.Contains(out, "vm.started") {
		t.Errorf("vm.started should not match snapshot. prefix:\n%s", out)
	}
}

func TestFollow_AuthHeaderForwarded(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "secret-key", eventFilter{}, eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.authHeaders) == 0 || srv.authHeaders[0] != "Bearer secret-key" {
		t.Errorf("expected Authorization: Bearer secret-key, got %v", srv.authHeaders)
	}
}

func TestFollow_AuthFailureIsFatal(t *testing.T) {
	srv := newSSETestServer(t, nil)
	srv.statusOnce = http.StatusUnauthorized

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var buf bytes.Buffer
	err := followEventsStream(ctx, srv.URL(), "", eventFilter{}, eventRowOptions{}, &buf)
	if err == nil {
		t.Fatal("expected fatal auth error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("expected auth-failed error, got: %v", err)
	}
}

func TestFollow_GoneIsFatal(t *testing.T) {
	srv := newSSETestServer(t, nil)
	srv.statusOnce = http.StatusGone

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var buf bytes.Buffer
	err := followEventsStream(ctx, srv.URL(), "", eventFilter{}, eventRowOptions{}, &buf)
	if err == nil {
		t.Fatal("expected 410 fatal error")
	}
	if !strings.Contains(err.Error(), "replay window exceeded") {
		t.Errorf("expected replay-window error, got: %v", err)
	}
}

func TestFollow_ReconnectsOnDisconnect(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()},
	})
	srv.replay = []*types.Event{
		{ID: "2", Type: "vm.stopped", VMID: "vm-A", OccurredAt: time.Now()},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "", eventFilter{}, eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "vm.started") {
		t.Errorf("expected first connect events:\n%s", out)
	}
	if !strings.Contains(out, "vm.stopped") {
		t.Errorf("expected reconnect (replay) events:\n%s", out)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	// Must have seen at least one Last-Event-ID header on a reconnect.
	saw := false
	for _, id := range srv.lastIDSeen {
		if id == "1" {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected Last-Event-ID=1 on reconnect, headers seen: %v", srv.lastIDSeen)
	}
}

func TestFollow_HeartbeatIgnored(t *testing.T) {
	// Custom server that emits a heartbeat then one event then closes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f.Flush()
		fmt.Fprint(w, ": keepalive\n\n")
		f.Flush()
		evt := &types.Event{ID: "1", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()}
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", evt.ID, evt.Type, data)
		f.Flush()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL, "", eventFilter{}, eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}
	if !strings.Contains(buf.String(), "vm.started") {
		t.Errorf("event after heartbeat missing:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "keepalive") {
		t.Errorf("heartbeat comment should not be printed:\n%s", buf.String())
	}
}

func TestLastIDToSeq(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"42", "42", false},
		{"0", "0", false},
		{"evt-1234", "", true},
		{"", "", true},
		{"abc", "", true},
	}
	for _, c := range cases {
		got, err := lastIDToSeq(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("lastIDToSeq(%q) err=%v want err=%v", c.in, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("lastIDToSeq(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ============================================================
// `vmsmith events list --sort --order` (5.4.16)
// ============================================================

func TestCLI_EventsList_SortByType_CaseInsensitive(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	base := time.Now()
	s.PutEvent(&types.Event{ID: "1", Type: "vm.started", CreatedAt: base})
	s.PutEvent(&types.Event{ID: "2", Type: "Image.created", CreatedAt: base})
	s.PutEvent(&types.Event{ID: "3", Type: "snapshot.taken", CreatedAt: base})

	out, err := runCLI("events", "list", "--sort", "type", "--order", "asc")
	if err != nil {
		t.Fatalf("events list --sort: %v", err)
	}
	// case-insensitive: "image.created" < "snapshot.taken" < "vm.started"
	idxImage := strings.Index(out, "Image.created")
	idxSnap := strings.Index(out, "snapshot.taken")
	idxStart := strings.Index(out, "vm.started")
	if !(idxImage >= 0 && idxSnap >= 0 && idxStart >= 0 && idxImage < idxSnap && idxSnap < idxStart) {
		t.Errorf("expected type asc order image<snapshot<vm; got positions %d/%d/%d:\n%s",
			idxImage, idxSnap, idxStart, out)
	}
}

func TestCLI_EventsList_SortByOccurredAtAsc(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	s.PutEvent(&types.Event{ID: "1", Type: "alpha", OccurredAt: base.Add(2 * time.Hour)})
	s.PutEvent(&types.Event{ID: "2", Type: "bravo", OccurredAt: base.Add(1 * time.Hour)})
	s.PutEvent(&types.Event{ID: "3", Type: "charlie", OccurredAt: base.Add(3 * time.Hour)})

	out, err := runCLI("events", "list", "--sort", "occurred_at", "--order", "asc")
	if err != nil {
		t.Fatalf("events list --sort: %v", err)
	}
	idxBravo := strings.Index(out, "bravo")
	idxAlpha := strings.Index(out, "alpha")
	idxCharlie := strings.Index(out, "charlie")
	if !(idxBravo >= 0 && idxAlpha >= 0 && idxCharlie >= 0 && idxBravo < idxAlpha && idxAlpha < idxCharlie) {
		t.Errorf("expected occurred_at asc order bravo<alpha<charlie; got positions %d/%d/%d:\n%s",
			idxBravo, idxAlpha, idxCharlie, out)
	}
}

func TestCLI_EventsList_RejectsInvalidSort(t *testing.T) {
	_, cleanup := withTestEventStore(t)
	defer cleanup()

	// `attributes` was the historical canary but actor is now a real axis
	// (5.4.87) so the unknown-canary needs to be something clearly absent
	// from the whitelist.
	_, err := runCLI("events", "list", "--sort", "attribute_keys")
	if err == nil {
		t.Fatal("expected error for invalid --sort")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Errorf("wrong error: %v", err)
	}
	// Error message must advertise the full supported set so operators
	// don't have to guess what landed in the whitelist; the 5.4.87 sweep
	// added `actor`.
	if !strings.Contains(err.Error(), "actor") {
		t.Errorf("error message must advertise actor: %v", err)
	}
}

func TestCLI_EventsList_SortByActor_AscEmptyTrailing(t *testing.T) {
	// Empty actor sinks to the tail of `asc`, concrete actors sort
	// case-sensitively. Mirrors the API contract.
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	base := time.Now()
	s.PutEvent(&types.Event{ID: "1", Type: "alpha", Actor: "system", CreatedAt: base})
	s.PutEvent(&types.Event{ID: "2", Type: "bravo", Actor: "", CreatedAt: base})
	s.PutEvent(&types.Event{ID: "3", Type: "charlie", Actor: "app", CreatedAt: base})

	out, err := runCLI("events", "list", "--sort", "actor", "--order", "asc")
	if err != nil {
		t.Fatalf("events list --sort actor: %v", err)
	}
	// asc: app(charlie) < system(alpha) < empty(bravo)
	idxCharlie := strings.Index(out, "charlie")
	idxAlpha := strings.Index(out, "alpha")
	idxBravo := strings.Index(out, "bravo")
	if !(idxCharlie >= 0 && idxAlpha >= 0 && idxBravo >= 0 && idxCharlie < idxAlpha && idxAlpha < idxBravo) {
		t.Errorf("expected actor asc order charlie<alpha<bravo (empty trails); got positions %d/%d/%d:\n%s",
			idxCharlie, idxAlpha, idxBravo, out)
	}
}

func TestCLI_EventsList_RejectsInvalidOrder(t *testing.T) {
	_, cleanup := withTestEventStore(t)
	defer cleanup()

	_, err := runCLI("events", "list", "--sort", "type", "--order", "sideways")
	if err == nil {
		t.Fatal("expected error for invalid --order")
	}
	if !strings.Contains(err.Error(), "invalid --order") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestCLI_EventsList_NoSortFlagPreservesLegacyNewestFirst(t *testing.T) {
	// Without --sort the CLI must still order by timestamp desc (legacy),
	// independent of the API's id-desc default.
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	// Insert id-asc != time-desc so the wrong path is observable.
	s.PutEvent(&types.Event{ID: "1", Type: "first_inserted", OccurredAt: base.Add(1 * time.Hour)})
	s.PutEvent(&types.Event{ID: "2", Type: "second_inserted", OccurredAt: base.Add(3 * time.Hour)})
	s.PutEvent(&types.Event{ID: "3", Type: "third_inserted", OccurredAt: base.Add(2 * time.Hour)})

	out, err := runCLI("events", "list")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	// expect: second(3h) < third(2h) < first(1h)
	idxSecond := strings.Index(out, "second_inserted")
	idxThird := strings.Index(out, "third_inserted")
	idxFirst := strings.Index(out, "first_inserted")
	if !(idxSecond < idxThird && idxThird < idxFirst) {
		t.Errorf("expected newest-by-timestamp; got positions second=%d third=%d first=%d:\n%s",
			idxSecond, idxThird, idxFirst, out)
	}
}

func TestMatchesEventFilter(t *testing.T) {
	e := &types.Event{Type: "vm.started", Source: "libvirt", Severity: "info", VMID: "vm-A"}
	cases := []struct {
		name  string
		f     eventFilter
		match bool
	}{
		{"empty filter matches", eventFilter{}, true},
		{"vm match", eventFilter{vmID: "vm-A"}, true},
		{"vm mismatch", eventFilter{vmID: "vm-B"}, false},
		{"type match", eventFilter{typeStr: "vm.started"}, true},
		{"type mismatch", eventFilter{typeStr: "vm.stopped"}, false},
		{"type-prefix family match", eventFilter{typePrefix: "vm."}, true},
		{"type-prefix full type match", eventFilter{typePrefix: "vm.started"}, true},
		{"type-prefix mismatch", eventFilter{typePrefix: "snapshot."}, false},
		{"type-prefix case-insensitive", eventFilter{typePrefix: "vm."}, true},
		{"source case-insensitive", eventFilter{source: "LIBVIRT"}, true},
		{"severity case-insensitive", eventFilter{severity: "INFO"}, true},
		{"severity mismatch", eventFilter{severity: "error"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchesEventFilter(e, c.f); got != c.match {
				t.Errorf("got %v, want %v", got, c.match)
			}
		})
	}
}

// --- --actor / --attrs column tests ---

// seedRichEvent stores an event with every structured detail field set so the
// rendering tests can assert each field appears (or not) in the table output.
func seedRichEvent(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.PutEvent(&types.Event{
		ID:         "evt-rich",
		Type:       "vm.created",
		Source:     "app",
		Severity:   "info",
		VMID:       "vm-100",
		ResourceID: "tpl-rocky9",
		Actor:      "alice@example.com",
		Message:    "VM created from template",
		Attributes: map[string]string{
			"template": "rocky9-base",
			"cpus":     "4",
			"ram_mb":   "8192",
		},
		OccurredAt: time.Now(),
	}); err != nil {
		t.Fatalf("PutEvent: %v", err)
	}
}

func TestCLI_EventsList_DefaultHidesActorAndAttrsColumns(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedRichEvent(t, s)

	out, err := runCLI("events", "list")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	// Header must be the legacy 6-column shape — existing scripted callers
	// that grep for column names cannot regress.
	if !strings.Contains(out, "TIME") || !strings.Contains(out, "MESSAGE") {
		t.Fatalf("missing base header columns:\n%s", out)
	}
	if strings.Contains(out, "ACTOR") {
		t.Errorf("ACTOR header should be hidden without --show-actor:\n%s", out)
	}
	if strings.Contains(out, "ATTRIBUTES") {
		t.Errorf("ATTRIBUTES header should be hidden without --show-attrs:\n%s", out)
	}
	// Without --show-actor, the alice@ value must not appear either.
	if strings.Contains(out, "alice@example.com") {
		t.Errorf("actor leaked into default output:\n%s", out)
	}
	// Without --show-attrs, neither the attribute keys nor the resource_id should leak.
	if strings.Contains(out, "template=rocky9-base") || strings.Contains(out, "resource_id=tpl-rocky9") {
		t.Errorf("attributes leaked into default output:\n%s", out)
	}
}

func TestCLI_EventsList_ShowActorFlagAddsActorColumn(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedRichEvent(t, s)

	out, err := runCLI("events", "list", "--show-actor")
	if err != nil {
		t.Fatalf("events list --show-actor: %v", err)
	}
	if !strings.Contains(out, "ACTOR") {
		t.Errorf("--show-actor should print ACTOR header:\n%s", out)
	}
	if !strings.Contains(out, "alice@example.com") {
		t.Errorf("--show-actor should print actor value:\n%s", out)
	}
	// --show-actor alone should NOT add the attributes column.
	if strings.Contains(out, "ATTRIBUTES") || strings.Contains(out, "template=rocky9-base") {
		t.Errorf("--show-actor alone should not surface attributes:\n%s", out)
	}
}

func TestCLI_EventsList_ShowAttrsFlagAddsAttributesColumn(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedRichEvent(t, s)

	out, err := runCLI("events", "list", "--show-attrs")
	if err != nil {
		t.Fatalf("events list --show-attrs: %v", err)
	}
	if !strings.Contains(out, "ATTRIBUTES") {
		t.Errorf("--show-attrs should print ATTRIBUTES header:\n%s", out)
	}
	// Keys must appear sorted alphabetically (cpus < ram_mb < template).
	wantOrder := []string{"cpus=4", "ram_mb=8192", "template=rocky9-base"}
	last := -1
	for _, want := range wantOrder {
		idx := strings.Index(out, want)
		if idx == -1 {
			t.Fatalf("--attrs missing key %q in output:\n%s", want, out)
		}
		if idx < last {
			t.Errorf("--attrs keys not alphabetical: %q at %d came after a later key:\n%s", want, idx, out)
		}
		last = idx
	}
	// resource_id should be folded into the attributes column when set.
	if !strings.Contains(out, "resource_id=tpl-rocky9") {
		t.Errorf("--show-attrs should fold in resource_id:\n%s", out)
	}
	// --show-attrs alone should NOT add the actor column.
	if strings.Contains(out, "ACTOR") || strings.Contains(out, "alice@example.com") {
		t.Errorf("--show-attrs alone should not surface actor:\n%s", out)
	}
}

func TestCLI_EventsList_ShowActorAndShowAttrsBothEnabled(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	seedRichEvent(t, s)

	out, err := runCLI("events", "list", "--show-actor", "--show-attrs")
	if err != nil {
		t.Fatalf("events list --show-actor --show-attrs: %v", err)
	}
	if !strings.Contains(out, "ACTOR") || !strings.Contains(out, "ATTRIBUTES") {
		t.Errorf("both columns should be present:\n%s", out)
	}
	// ACTOR header must come before VM/MESSAGE; ATTRIBUTES must come last.
	idxActor := strings.Index(out, "ACTOR")
	idxVM := strings.Index(out, "VM")
	idxMessage := strings.Index(out, "MESSAGE")
	idxAttrs := strings.Index(out, "ATTRIBUTES")
	if !(idxActor < idxVM && idxVM < idxMessage && idxMessage < idxAttrs) {
		t.Errorf("column order wrong (want ACTOR < VM < MESSAGE < ATTRIBUTES); got actor=%d vm=%d message=%d attrs=%d:\n%s",
			idxActor, idxVM, idxMessage, idxAttrs, out)
	}
}

func TestCLI_EventsList_AttrsDashOnEmptyAttributes(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()
	if err := s.PutEvent(&types.Event{
		ID:         "evt-bare",
		Type:       "vm.stopped",
		Source:     "libvirt",
		Severity:   "info",
		VMID:       "vm-200",
		Message:    "VM stopped cleanly",
		OccurredAt: time.Now(),
	}); err != nil {
		t.Fatalf("PutEvent: %v", err)
	}

	out, err := runCLI("events", "list", "--show-attrs")
	if err != nil {
		t.Fatalf("events list --show-attrs: %v", err)
	}
	// The events table writes one row per event; verify there's a tabwriter
	// row for vm.stopped (so the empty-attrs case doesn't silently drop it).
	if !strings.Contains(out, "vm.stopped") {
		t.Fatalf("missing event row:\n%s", out)
	}
	// And — importantly — no spurious resource_id= or key=value tokens.
	if strings.Contains(out, "resource_id=") || strings.Contains(out, "=") && strings.Count(out, "=") > 0 {
		// Be lenient: the only `=` could come from the path of an unset
		// attribute. Confirm by checking that none of the seeded keys appear.
		for _, banned := range []string{"template=", "cpus=", "ram_mb=", "resource_id="} {
			if strings.Contains(out, banned) {
				t.Errorf("empty-attrs row leaked a key %q:\n%s", banned, out)
			}
		}
	}
}

func TestFormatEventAttributes(t *testing.T) {
	cases := []struct {
		name string
		evt  *types.Event
		want string
	}{
		{
			name: "empty",
			evt:  &types.Event{},
			want: "-",
		},
		{
			name: "resource_id only",
			evt:  &types.Event{ResourceID: "img-1"},
			want: "resource_id=img-1",
		},
		{
			name: "attrs sorted alphabetically",
			evt: &types.Event{Attributes: map[string]string{
				"zebra": "stripe",
				"alpha": "first",
				"mid":   "x",
			}},
			want: "alpha=first mid=x zebra=stripe",
		},
		{
			name: "resource_id prepended before attrs",
			evt: &types.Event{
				ResourceID: "tpl-9",
				Attributes: map[string]string{"a": "1", "b": "2"},
			},
			want: "resource_id=tpl-9 a=1 b=2",
		},
		{
			name: "whitespace-only resource_id ignored",
			evt: &types.Event{
				ResourceID: "   ",
				Attributes: map[string]string{"k": "v"},
			},
			want: "k=v",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatEventAttributes(c.evt); got != c.want {
				t.Errorf("formatEventAttributes() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestCLI_EventsFollow_FiltersByActor checks that the client-side actor
// predicate filters the SSE stream so operators tailing one human / bot
// don't have to wade through every event arriving on the wire.
func TestCLI_EventsFollow_FiltersByActor(t *testing.T) {
	now := time.Now()
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", Source: "app", Severity: "info", Actor: "ops-alice", VMID: "vm-1", Message: "alice start", OccurredAt: now},
		{ID: "2", Type: "vm.stopped", Source: "app", Severity: "info", Actor: "ops-bob", VMID: "vm-1", Message: "bob stop", OccurredAt: now.Add(time.Second)},
		{ID: "3", Type: "vm.deleted", Source: "app", Severity: "info", Actor: "ops-alice", VMID: "vm-1", Message: "alice delete", OccurredAt: now.Add(2 * time.Second)},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "",
		eventFilter{actor: "ops-alice"},
		eventRowOptions{showActor: true}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "alice start") || !strings.Contains(out, "alice delete") {
		t.Errorf("alice rows missing from follow output:\n%s", out)
	}
	if strings.Contains(out, "bob stop") {
		t.Errorf("bob row should have been filtered out:\n%s", out)
	}
}

// TestCLI_EventsFollow_FiltersByMinSeverity exercises the SSE path so the
// `events follow --min-severity` floor stays in lockstep with `events list`.
func TestCLI_EventsFollow_FiltersByMinSeverity(t *testing.T) {
	now := time.Now()
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.created", Source: "app", Severity: "info", VMID: "vm-1", Message: "info row", OccurredAt: now},
		{ID: "2", Type: "vm.stopped", Source: "libvirt", Severity: "warn", VMID: "vm-1", Message: "warn row", OccurredAt: now.Add(time.Second)},
		{ID: "3", Type: "dhcp.exhausted", Source: "system", Severity: "error", Message: "error row", OccurredAt: now.Add(2 * time.Second)},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "",
		eventFilter{minSeverity: "warn"},
		eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "warn row") || !strings.Contains(out, "error row") {
		t.Errorf("warn floor should keep warn + error rows:\n%s", out)
	}
	if strings.Contains(out, "info row") {
		t.Errorf("warn floor should drop info row:\n%s", out)
	}
}

// TestCLI_EventsFollow_AttrsFlagAddsAttributesColumn exercises the SSE path so
// the `events follow` rendering doesn't silently fall behind `events list` when
// new columns are added in the future.
func TestCLI_EventsFollow_AttrsAndActorFlagsAddColumns(t *testing.T) {
	now := time.Now()
	srv := newSSETestServer(t, []*types.Event{
		{
			ID:         "1",
			Type:       "vm.created",
			Source:     "app",
			Severity:   "info",
			VMID:       "vm-9",
			ResourceID: "tpl-rocky9",
			Actor:      "ops-alice",
			Message:    "VM created",
			Attributes: map[string]string{"template": "rocky9-base", "cpus": "4"},
			OccurredAt: now,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "", eventFilter{},
		eventRowOptions{showActor: true, showAttrs: true}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "ops-alice") {
		t.Errorf("--actor missing from follow output:\n%s", out)
	}
	if !strings.Contains(out, "cpus=4") || !strings.Contains(out, "template=rocky9-base") {
		t.Errorf("--attrs missing from follow output:\n%s", out)
	}
	if !strings.Contains(out, "resource_id=tpl-rocky9") {
		t.Errorf("--attrs should fold in resource_id in follow output:\n%s", out)
	}
}

// TestCLI_EventsFollow_FiltersByResourceID exercises the SSE path so the
// `events follow --resource-id` filter stays in lockstep with `events list`.
func TestCLI_EventsFollow_FiltersByResourceID(t *testing.T) {
	now := time.Now()
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "snapshot.created", Source: "app", ResourceID: "snap-prod-pre", VMID: "vm-1", Message: "made", OccurredAt: now},
		{ID: "2", Type: "image.uploaded", Source: "app", ResourceID: "img-other", Message: "uploaded", OccurredAt: now},
		{ID: "3", Type: "vm.started", Source: "libvirt", VMID: "vm-1", Message: "started", OccurredAt: now},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "",
		eventFilter{resourceID: "snap-prod-pre"},
		eventRowOptions{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "snapshot.created") {
		t.Errorf("matching event missing from follow output:\n%s", out)
	}
	if strings.Contains(out, "image.uploaded") || strings.Contains(out, "vm.started") {
		t.Errorf("--resource-id filter leaked in follow output:\n%s", out)
	}
}

// TestCLI_EventsFollow_ForwardsFiltersAsQueryParams pins the contract that
// `events follow` pushes the filter set to the daemon as query params so the
// SSE server-side predicate (5.4.33) can short-circuit non-matching events
// before they cross the wire.
func TestCLI_EventsFollow_ForwardsFiltersAsQueryParams(t *testing.T) {
	srv := newSSETestServer(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	_ = followEventsStream(ctx, srv.URL(), "",
		eventFilter{
			vmID:       "vm-42",
			typeStr:    "vm.started",
			typePrefix: "vm.",
			source:     "app",
			severity:   "info",
			actor:      "ops-alice",
			resourceID: "snap-1",
			search:     "needle",
		},
		eventRowOptions{}, &buf)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.queries) == 0 {
		t.Fatal("no requests captured")
	}
	q := srv.queries[0]
	checks := map[string]string{
		"vm_id":       "vm-42",
		"type":        "vm.started",
		"type_prefix": "vm.",
		"source":      "app",
		"severity":    "info",
		"actor":       "ops-alice",
		"resource_id": "snap-1",
		"search":      "needle",
	}
	for key, want := range checks {
		if got := q.Get(key); got != want {
			t.Errorf("query param %q = %q, want %q (full = %v)", key, got, want, q)
		}
	}
}

// TestCLI_EventsFollow_EmptyFilterOmitsQueryParams verifies the default
// follow path emits no filter params, preserving the pre-5.4.33 wire
// behaviour for users who don't want server-side filtering.
func TestCLI_EventsFollow_EmptyFilterOmitsQueryParams(t *testing.T) {
	srv := newSSETestServer(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	_ = followEventsStream(ctx, srv.URL(), "", eventFilter{}, eventRowOptions{}, &buf)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.queries) == 0 {
		t.Fatal("no requests captured")
	}
	q := srv.queries[0]
	for _, key := range []string{"vm_id", "type", "type_prefix", "source", "severity", "actor", "resource_id", "search"} {
		if got := q.Get(key); got != "" {
			t.Errorf("expected no %q param when filter is empty, got %q (full = %v)", key, got, q)
		}
	}
}
