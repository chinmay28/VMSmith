package console

import (
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

const (
	defaultTicketTTL       = 60 * time.Second
	defaultJanitorInterval = 30 * time.Second
	ticketEntropyBytes     = 32
)

var (
	ErrTicketNotFound   = errors.New("console ticket not found")
	ErrTicketExpired    = errors.New("console ticket expired")
	ErrTicketVMMismatch = errors.New("console ticket vm mismatch")
)

type ticket struct {
	vmID    string
	apiKey  string
	expires time.Time
}

// Store keeps one-time console tickets in memory.
type Store struct {
	mu              sync.RWMutex
	tickets         map[string]ticket
	now             func() time.Time
	ttl             time.Duration
	janitorInterval time.Duration
	stopJanitor     context.CancelFunc
}

// NewStore creates a ticket store with the default TTL and janitor interval.
func NewStore() *Store {
	return NewStoreWithOptions(defaultTicketTTL, defaultJanitorInterval)
}

// NewStoreWithOptions creates a ticket store with explicit timing options.
func NewStoreWithOptions(ttl, janitorInterval time.Duration) *Store {
	if ttl <= 0 {
		ttl = defaultTicketTTL
	}
	if janitorInterval <= 0 {
		janitorInterval = defaultJanitorInterval
	}

	s := &Store{
		tickets:         make(map[string]ticket),
		now:             time.Now,
		ttl:             ttl,
		janitorInterval: janitorInterval,
	}
	s.startJanitor()
	return s
}

// Close stops the janitor goroutine.
func (s *Store) Close() {
	if s == nil || s.stopJanitor == nil {
		return
	}
	s.stopJanitor()
	s.stopJanitor = nil
}

// IssueTicket creates a new single-use ticket for the given VM and API key.
func (s *Store) IssueTicket(vmID, apiKey string) (string, time.Time, error) {
	token, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}

	expires := s.now().Add(s.ttl)

	s.mu.Lock()
	s.tickets[token] = ticket{
		vmID:    vmID,
		apiKey:  apiKey,
		expires: expires,
	}
	s.mu.Unlock()

	return token, expires, nil
}

// ConsumeTicket validates and removes a ticket.
func (s *Store) ConsumeTicket(token, vmID string) (string, error) {
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tickets[token]
	if !ok {
		return "", ErrTicketNotFound
	}
	delete(s.tickets, token)

	if !now.Before(t.expires) {
		return "", ErrTicketExpired
	}
	if t.vmID != vmID {
		return "", ErrTicketVMMismatch
	}

	return t.apiKey, nil
}

func (s *Store) startJanitor() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stopJanitor = cancel

	go func() {
		ticker := time.NewTicker(s.janitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.deleteExpired(s.now())
			}
		}
	}()
}

func (s *Store) deleteExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, t := range s.tickets {
		if !now.Before(t.expires) {
			delete(s.tickets, token)
		}
	}
}

func newToken() (string, error) {
	buf := make([]byte, ticketEntropyBytes)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
