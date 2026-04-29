package eventstore

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
)

type MemoryStore struct {
	mu          sync.RWMutex
	events      map[string][]Event
	subscribers []Subscriber
	registry    map[string]func() any
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		events:   make(map[string][]Event),
		registry: make(map[string]func() any),
	}
}

// RegisterEvent maps an event type string to a factory that creates a zero-value
// pointer for JSON unmarshalling. Call this at startup for each domain event type.
func (s *MemoryStore) RegisterEvent(eventType string, factory func() any) {
	s.registry[eventType] = factory
}

func (s *MemoryStore) Subscribe(sub Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers = append(s.subscribers, sub)
}

func (s *MemoryStore) notify(events []Event) {
	for _, e := range events {
		factory, ok := s.registry[e.EventType]
		if !ok {
			continue
		}
		ptr := factory()
		if err := json.Unmarshal(e.Payload, ptr); err != nil {
			continue
		}
		// Dereference pointer so projectors receive value types (e.g. WalletAdded, not *WalletAdded)
		val := reflect.ValueOf(ptr).Elem().Interface()
		for _, sub := range s.subscribers {
			sub.Handle(val)
		}
	}
}

func (s *MemoryStore) Append(_ context.Context, aggregateID string, events []Event, expectedVersion int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.events[aggregateID]
	currentVersion := len(existing)

	if currentVersion != expectedVersion {
		return ErrConcurrencyConflict
	}

	for i, e := range events {
		e.Version = currentVersion + i + 1
		existing = append(existing, e)
	}

	s.events[aggregateID] = existing
	s.notify(events)
	return nil
}

func (s *MemoryStore) AppendWithOutbox(_ context.Context, aggregateID string, events []Event, expectedVersion int, _ []OutboxEntry) error {
	// In-memory implementation ignores outbox entries — they only matter for PostgreSQL.
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.events[aggregateID]
	currentVersion := len(existing)

	if currentVersion != expectedVersion {
		return ErrConcurrencyConflict
	}

	for i, e := range events {
		e.Version = currentVersion + i + 1
		existing = append(existing, e)
	}

	s.events[aggregateID] = existing
	s.notify(events)
	return nil
}

func (s *MemoryStore) AppendMulti(_ context.Context, batches []AggregateEvents) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Phase 1: validate all versions before writing anything
	for _, batch := range batches {
		existing := s.events[batch.AggregateID]
		if len(existing) != batch.ExpectedVersion {
			return ErrConcurrencyConflict
		}
	}

	// Phase 2: all valid — append all events atomically
	var allEvents []Event
	for _, batch := range batches {
		existing := s.events[batch.AggregateID]
		currentVersion := len(existing)
		for i, e := range batch.Events {
			e.Version = currentVersion + i + 1
			existing = append(existing, e)
		}
		s.events[batch.AggregateID] = existing
		allEvents = append(allEvents, batch.Events...)
	}

	s.notify(allEvents)
	return nil
}

func (s *MemoryStore) Load(_ context.Context, aggregateID string) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events, ok := s.events[aggregateID]
	if !ok || len(events) == 0 {
		return nil, ErrAggregateNotFound
	}

	result := make([]Event, len(events))
	copy(result, events)
	return result, nil
}

func (s *MemoryStore) LoadAll(_ context.Context) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var all []Event
	for _, events := range s.events {
		all = append(all, events...)
	}
	return all, nil
}

func (s *MemoryStore) ReplayAll(_ context.Context) error {
	s.mu.RLock()
	var all []Event
	for _, events := range s.events {
		all = append(all, events...)
	}
	s.mu.RUnlock()

	s.notify(all)
	return nil
}
