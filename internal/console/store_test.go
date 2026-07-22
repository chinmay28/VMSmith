package console

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !fn() {
		t.Fatal("condition not met before timeout")
	}
}

func TestIssueAndConsumeTicket(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	token, expiresAt, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if expiresAt.IsZero() {
		t.Fatal("expected non-zero expiry")
	}

	apiKey, err := s.ConsumeTicket(token, "vm-1", "vnc")
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

	token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	if _, err := s.ConsumeTicket(token, "vm-1", "vnc"); err != nil {
		t.Fatalf("first ConsumeTicket returned error: %v", err)
	}
	if _, err := s.ConsumeTicket(token, "vm-1", "vnc"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("second ConsumeTicket error = %v, want %v", err, ErrTicketNotFound)
	}
}

func TestConsumeTicketExpired(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	s.now = func() time.Time { return now.Add(time.Minute + time.Second) }

	if _, err := s.ConsumeTicket(token, "vm-1", "vnc"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("ConsumeTicket error = %v, want %v", err, ErrTicketExpired)
	}
}

func TestConsumeTicket_ExpiryBoundaryIsExpired(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	token, expiresAt, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	s.now = func() time.Time { return expiresAt }

	if _, err := s.ConsumeTicket(token, "vm-1", "vnc"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("ConsumeTicket at exact expiry error = %v, want %v", err, ErrTicketExpired)
	}
}

func TestConsumeTicketVMMismatch(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	if _, err := s.ConsumeTicket(token, "vm-2", "vnc"); !errors.Is(err, ErrTicketVMMismatch) {
		t.Fatalf("ConsumeTicket error = %v, want %v", err, ErrTicketVMMismatch)
	}
	if _, err := s.ConsumeTicket(token, "vm-1", "vnc"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("ticket should be removed after mismatch, got %v", err)
	}
}

func TestJanitorRemovesExpiredTickets(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	s.deleteExpired(now.Add(time.Minute + time.Second))

	if _, err := s.ConsumeTicket(token, "vm-1", "vnc"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("ConsumeTicket error = %v, want %v", err, ErrTicketNotFound)
	}
}

func TestJanitorRemovesExpiredTicketsAutomatically(t *testing.T) {
	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	s := NewStoreWithOptions(20*time.Millisecond, 10*time.Millisecond)
	defer s.Close()
	s.now = func() time.Time { return now }

	token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	now = now.Add(time.Minute)
	waitForCondition(t, 250*time.Millisecond, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		_, ok := s.tickets[token]
		return !ok
	})
}

func TestCloseStopsJanitor(t *testing.T) {
	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	s := NewStoreWithOptions(20*time.Millisecond, 10*time.Millisecond)
	s.now = func() time.Time { return now }

	token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	s.Close()
	now = now.Add(time.Minute)
	time.Sleep(50 * time.Millisecond)

	s.mu.RLock()
	_, ok := s.tickets[token]
	s.mu.RUnlock()
	if !ok {
		t.Fatal("ticket was removed even though the janitor was closed")
	}
}

func TestIssueTicketGeneratesUniqueTokens(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	const count = 32
	seen := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
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

	token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
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
			_, err := s.ConsumeTicket(token, "vm-1", "vnc")
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

func TestConsumeTicketIntentMismatch(t *testing.T) {
	s := NewStoreWithOptions(time.Minute, time.Hour)
	defer s.Close()

	token, _, err := s.IssueTicket("vm-1", "api-key-1", "vnc")
	if err != nil {
		t.Fatalf("IssueTicket returned error: %v", err)
	}

	if _, err := s.ConsumeTicket(token, "vm-1", "serial"); !errors.Is(err, ErrTicketIntentMismatch) {
		t.Fatalf("ConsumeTicket error = %v, want %v", err, ErrTicketIntentMismatch)
	}
	// Intent mismatch still burns the ticket — single-use above all.
	if _, err := s.ConsumeTicket(token, "vm-1", "vnc"); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("ConsumeTicket after mismatch error = %v, want %v", err, ErrTicketNotFound)
	}
}
