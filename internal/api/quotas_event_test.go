package api

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// TestCreateVM_QuotaExceeded_EmitsSystemEvent verifies that a create attempt
// rejected for quota reasons emits a `quota.exceeded` system event with
// structured attributes alongside the HTTP 429 response.
func TestCreateVM_QuotaExceeded_EmitsSystemEvent(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxVMs = 1
	})
	defer cleanup()

	// Seed one VM so the next create breaches the quota.
	mockMgr.SeedVM(&types.VM{ID: "vm-existing", Name: "existing", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DiskGB: 10}})

	_, ch, stop := wireEventBus(t, ts)
	defer stop()

	body := []byte(`{"name":"vmA","image":"rocky9.qcow2","cpus":1,"ram_mb":512,"disk_gb":10,"ssh_pub_key":"ssh-rsa AAAA test"}`)
	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for quota_exceeded, got %d", resp.StatusCode)
	}

	got := drainEvents(t, ch, 1)
	if len(got) != 1 {
		t.Fatalf("want 1 system event, got %d: %+v", len(got), got)
	}
	evt := got[0]
	if evt.Type != "quota.exceeded" {
		t.Errorf("Type = %q, want quota.exceeded", evt.Type)
	}
	if evt.Source != types.EventSourceSystem {
		t.Errorf("Source = %q, want %q", evt.Source, types.EventSourceSystem)
	}
	if evt.Severity != types.EventSeverityWarn {
		t.Errorf("Severity = %q, want warn", evt.Severity)
	}
	if evt.Attributes["field"] != "max_vms" {
		t.Errorf("attributes.field = %q, want max_vms", evt.Attributes["field"])
	}
	if evt.Attributes["limit"] != "1" || evt.Attributes["attempted"] != "2" {
		t.Errorf("attributes = %+v, want limit=1 attempted=2", evt.Attributes)
	}
}

// TestUpdateVM_QuotaExceeded_EmitsSystemEvent verifies a patch that exceeds
// the CPU quota emits a corresponding `quota.exceeded` system event with the
// `max_total_cpus` field tag.
func TestUpdateVM_QuotaExceeded_EmitsSystemEvent(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxTotalCPUs = 4
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "vm1", Spec: types.VMSpec{CPUs: 2, RAMMB: 512, DiskGB: 10}})

	_, ch, stop := wireEventBus(t, ts)
	defer stop()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-1",
		bytes.NewReader([]byte(`{"cpus":8}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for quota_exceeded, got %d", resp.StatusCode)
	}

	got := drainEvents(t, ch, 1)
	if len(got) != 1 || got[0].Type != "quota.exceeded" {
		t.Fatalf("want 1 quota.exceeded event, got %+v", got)
	}
	if got[0].Attributes["field"] != "max_total_cpus" {
		t.Errorf("attributes.field = %q, want max_total_cpus", got[0].Attributes["field"])
	}
}

// TestPublishSystemEvent_NoBus must not panic when no bus is wired.
func TestPublishSystemEvent_NoBus(t *testing.T) {
	s := &Server{}
	s.publishSystemEvent("quota.exceeded", types.EventSeverityWarn, "test", nil)
}
