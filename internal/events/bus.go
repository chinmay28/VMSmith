// Package events provides an in-process fan-out event bus for VM lifecycle
// and system events.  A single goroutine assigns monotonic IDs, persists to
// bbolt via the Store interface, and fans out to subscriber channels.
// Slow subscribers are dropped rather than blocking the libvirt event loop.
package events

import (
	"fmt"
	"sync"
	"time"

	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

const (
	subscriberBufSize  = 64
	publishBufSize     = 256
	dropWarnIntervalSec = 60
)

// Store is the minimal persistence interface the bus requires.
type Store interface {
	AppendEvent(event *types.Event) (uint64, error)
}

// EventBus is a fan-out broker.  Producers call Publish; consumers register
// via Subscribe.  A single background goroutine serializes ID assignment,
// persistence, and fan-out.
type EventBus struct {
	store     Store
	publishCh chan *types.Event

	mu          sync.RWMutex
	subscribers map[string]*subscriber

	dropWarnings sync.Map // name -> time.Time of last drop warning

	stopCh  chan struct{}
	stopped chan struct{}
}

type subscriber struct {
	name string
	ch   chan *types.Event
}

// New creates a new EventBus backed by store.  Call Start to begin processing.
func New(store Store) *EventBus {
	return &EventBus{
		store:       store,
		publishCh:   make(chan *types.Event, publishBufSize),
		subscribers: make(map[string]*subscriber),
		stopCh:      make(chan struct{}),
		stopped:     make(chan struct{}),
	}
}

// Start launches the processing goroutine.  It should be called once.
func (b *EventBus) Start() {
	go b.run()
}

// Stop signals the goroutine to drain and exit, then waits for it.
func (b *EventBus) Stop() {
	select {
	case <-b.stopCh:
	default:
		close(b.stopCh)
	}
	<-b.stopped
}

// Publish submits an event for ID assignment, persistence, and fan-out.
// It never blocks; if the internal buffer is full the event is dropped and
// a warning is logged.
func (b *EventBus) Publish(evt *types.Event) {
	if evt.OccurredAt.IsZero() {
		evt.OccurredAt = time.Now()
	}
	evt.CreatedAt = evt.OccurredAt // backward compat

	select {
	case b.publishCh <- evt:
	default:
		logger.Warn("events", "publish channel full, dropping event", "type", evt.Type)
	}
}

// Subscribe registers a subscriber and returns a channel that receives events
// plus a cancel function to deregister.  name must be unique per subscriber.
func (b *EventBus) Subscribe(name string) (<-chan *types.Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := &subscriber{
		name: name,
		ch:   make(chan *types.Event, subscriberBufSize),
	}
	b.subscribers[name] = sub

	cancel := func() {
		b.mu.Lock()
		delete(b.subscribers, name)
		b.mu.Unlock()
		// Drain to unblock any sender currently holding the lock in fanOut.
		for len(sub.ch) > 0 {
			<-sub.ch
		}
	}
	return sub.ch, cancel
}

// NewAppEvent is a convenience constructor for app-source events.
func NewAppEvent(evtType, vmID, message string, attrs map[string]string) *types.Event {
	return &types.Event{
		Type:       evtType,
		Source:     types.EventSourceApp,
		VMID:       vmID,
		Severity:   types.EventSeverityInfo,
		Message:    message,
		Attributes: attrs,
		OccurredAt: time.Now(),
	}
}

// NewSystemEvent is a convenience constructor for system-source events.
func NewSystemEvent(evtType, severity, message string) *types.Event {
	return &types.Event{
		Type:       evtType,
		Source:     types.EventSourceSystem,
		Severity:   severity,
		Message:    message,
		OccurredAt: time.Now(),
	}
}

func (b *EventBus) run() {
	defer close(b.stopped)
	for {
		select {
		case <-b.stopCh:
			// Drain the publish channel before exiting.
			for {
				select {
				case evt := <-b.publishCh:
					b.process(evt)
				default:
					return
				}
			}
		case evt := <-b.publishCh:
			b.process(evt)
		}
	}
}

func (b *EventBus) process(evt *types.Event) {
	// Persist and get the assigned sequence ID.
	seq, err := b.store.AppendEvent(evt)
	if err != nil {
		logger.Warn("events", "failed to persist event", "type", evt.Type, "error", err.Error())
		// Still fan out — in-memory consumers should see the event even if
		// persistence failed.
		evt.ID = fmt.Sprintf("transient-%d", time.Now().UnixNano())
	} else {
		evt.ID = fmt.Sprintf("%d", seq)
	}

	b.fanOut(evt)
}

func (b *EventBus) fanOut(evt *types.Event) {
	b.mu.RLock()
	subs := make([]*subscriber, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		subs = append(subs, s)
	}
	b.mu.RUnlock()

	for _, sub := range subs {
		select {
		case sub.ch <- evt:
		default:
			b.maybeWarnDrop(sub.name)
		}
	}
}

func (b *EventBus) maybeWarnDrop(name string) {
	now := time.Now()
	val, loaded := b.dropWarnings.LoadOrStore(name, now)
	if loaded {
		if now.Sub(val.(time.Time)).Seconds() < dropWarnIntervalSec {
			return
		}
		b.dropWarnings.Store(name, now)
	}
	logger.Warn("events", "slow subscriber dropping event", "subscriber", name)
}
