package console

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestIssueAndConsumeTicket(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	token, expiresAt, err := s.IssueTicket("vm-1", "api-key-1")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if expiresAt.IsZero() {
		t.Fatal("expected non-zero expiry")
	}

	apiKey, err := s.ConsumeTicket(token, "vm-1")
	if err != nil {
		t.Fatalf("ConsumeTicket returned error: %v", err)
	}
	if apiKey != "api-key-1" {
		t.Fatalf("ConsumeTicket returned api key %q, want %q", apiKey, "api-key-1")
	}
}

func TestConsumeTicketSingleUse(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	token, _, err := s.IssueTicket("vm-1", "api-key-1")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	if _, err := s.ConsumeTicket(token, "vm-1"); err != nil {
		t.Fatalf("first ConsumeTicket returned error: %v", err)
	}
	if _, err := s.ConsumeTicket(token, "vm-1"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("second ConsumeTicket error = %v, want %v", err, ErrTicketNotFound)
	}
}

func TestConsumeTicketExpired(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	token, _, err := s.IssueTicket("vm-1", "api-key-1")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	s.now = func() time.Time { return now.Add(time.Minute + time.Second) }

	if _, err := s.ConsumeTicket(token, "vm-1"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("ConsumeTicket error = %v, want %v", err, ErrTicketExpired)
	}
}

func TestConsumeTicketVMMismatch(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	token, _, err := s.IssueTicket("vm-1", "api-key-1")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	if _, err := s.ConsumeTicket(token, "vm-2"); !errors.Is(err, ErrTicketVMMismatch) {
		t.Fatalf("ConsumeTicket error = %v, want %v", err, ErrTicketVMMismatch)
	}
	if _, err := s.ConsumeTicket(token, "vm-1"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("ticket should be removed after mismatch, got %v", err)
	}
}

func TestJanitorRemovesExpiredTickets(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	token, _, err := s.IssueTicket("vm-1", "api-key-1")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	s.deleteExpired(now.Add(time.Minute + time.Second))

	if _, err := s.ConsumeTicket(token, "vm-1"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("ConsumeTicket error = %v, want %v", err, ErrTicketNotFound)
	}
}

func TestIssueTicketGeneratesUniqueTokens(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	const count = 32
	seen := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		token, _, err := s.IssueTicket("vm-1", "api-key-1")
		if err != nil {
			t.Fatalf("IssueTicket returned error: %v", err)
		}
		if _, ok := seen[token]; ok {
			t.Fatalf("duplicate token generated: %q", token)
		}
		seen[token] = struct{}{}
	}
}

func TestConsumeTicketConcurrentSingleWinner(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	token, _, err := s.IssueTicket("vm-1", "api-key-1")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	const attempts = 16
	results := make(chan error, attempts)

	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ConsumeTicket(token, "vm-1")
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	notFound := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrTicketNotFound):
			notFound++
		default:
			t.Fatalf("unexpected ConsumeTicket error: %v", err)
		}
	}

	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if notFound != attempts-1 {
		t.Fatalf("notFound = %d, want %d", notFound, attempts-1)
	}
}
