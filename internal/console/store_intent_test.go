package console

import (
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// TestTicketIntentRoundTrip covers the 5.1.9 intent plumbing: a ticket
// issued for one console flavour comes back with the same intent, and the
// legacy IssueTicket path defaults to vnc.
func TestTicketIntentRoundTrip(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	serialTok, _, err := s.IssueTicketIntent("vm-1", "key-1", types.ConsoleIntentSerial)
	if err != nil {
		t.Fatalf("issue serial: %v", err)
	}
	apiKey, intent, err := s.ConsumeTicketIntent(serialTok, "vm-1")
	if err != nil {
		t.Fatalf("consume serial: %v", err)
	}
	if apiKey != "key-1" || intent != types.ConsoleIntentSerial {
		t.Errorf("consume = (%q, %q), want (key-1, serial)", apiKey, intent)
	}

	vncTok, _, err := s.IssueTicket("vm-1", "key-2")
	if err != nil {
		t.Fatalf("issue vnc: %v", err)
	}
	apiKey, intent, err = s.ConsumeTicketIntent(vncTok, "vm-1")
	if err != nil {
		t.Fatalf("consume vnc: %v", err)
	}
	if apiKey != "key-2" || intent != types.ConsoleIntentVNC {
		t.Errorf("consume = (%q, %q), want (key-2, vnc)", apiKey, intent)
	}
}

// TestTicketIntentSingleUse confirms intent-carrying tickets keep the
// single-use contract.
func TestTicketIntentSingleUse(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	tok, _, err := s.IssueTicketIntent("vm-1", "k", types.ConsoleIntentSerial)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := s.ConsumeTicketIntent(tok, "vm-1"); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, _, err := s.ConsumeTicketIntent(tok, "vm-1"); err == nil {
		t.Error("second consume succeeded; tickets must be single-use")
	}
}
