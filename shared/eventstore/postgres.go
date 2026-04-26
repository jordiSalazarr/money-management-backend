package eventstore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool        *pgxpool.Pool
	mu          sync.RWMutex
	subscribers []Subscriber
	registry    map[string]func() any
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{
		pool:     pool,
		registry: make(map[string]func() any),
	}
}

func (s *PostgresStore) RegisterEvent(eventType string, factory func() any) {
	s.registry[eventType] = factory
}

func (s *PostgresStore) Subscribe(sub Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers = append(s.subscribers, sub)
}

func (s *PostgresStore) notify(events []Event) {
	for _, e := range events {
		factory, ok := s.registry[e.EventType]
		if !ok {
			continue
		}
		ptr := factory()
		if err := json.Unmarshal(e.Payload, ptr); err != nil {
			continue
		}
		val := reflect.ValueOf(ptr).Elem().Interface()
		for _, sub := range s.subscribers {
			sub.Handle(val)
		}
	}
}

func (s *PostgresStore) Append(ctx context.Context, aggregateID string, events []Event, expectedVersion int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	currentVersion, err := s.currentVersion(ctx, tx, aggregateID)
	if err != nil {
		return err
	}
	if currentVersion != expectedVersion {
		return ErrConcurrencyConflict
	}

	for i := range events {
		events[i].Version = currentVersion + i + 1
		if err := s.insertEvent(ctx, tx, events[i]); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	s.notify(events)
	return nil
}

func (s *PostgresStore) AppendMulti(ctx context.Context, batches []AggregateEvents) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Phase 1: validate all versions
	for _, batch := range batches {
		currentVersion, err := s.currentVersion(ctx, tx, batch.AggregateID)
		if err != nil {
			return err
		}
		if currentVersion != batch.ExpectedVersion {
			return ErrConcurrencyConflict
		}
	}

	// Phase 2: insert all events
	var allEvents []Event
	for _, batch := range batches {
		for i := range batch.Events {
			batch.Events[i].Version = batch.ExpectedVersion + i + 1
			if err := s.insertEvent(ctx, tx, batch.Events[i]); err != nil {
				return err
			}
		}
		allEvents = append(allEvents, batch.Events...)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	s.notify(allEvents)
	return nil
}

func (s *PostgresStore) Load(ctx context.Context, aggregateID string) ([]Event, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT event_id, aggregate_id, aggregate_type, event_type, version, correlation_id, payload, occurred_at
		 FROM events WHERE aggregate_id = $1 ORDER BY version`, aggregateID)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.EventID, &e.AggregateID, &e.AggregateType, &e.EventType, &e.Version, &e.CorrelationID, &e.Payload, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		events = append(events, e)
	}

	if len(events) == 0 {
		return nil, ErrAggregateNotFound
	}
	return events, nil
}

func (s *PostgresStore) LoadAll(ctx context.Context) ([]Event, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT event_id, aggregate_id, aggregate_type, event_type, version, correlation_id, payload, occurred_at
		 FROM events ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("querying all events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.EventID, &e.AggregateID, &e.AggregateType, &e.EventType, &e.Version, &e.CorrelationID, &e.Payload, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}

func (s *PostgresStore) ReplayAll(ctx context.Context) error {
	events, err := s.LoadAll(ctx)
	if err != nil {
		return err
	}
	s.notify(events)
	return nil
}

func (s *PostgresStore) currentVersion(ctx context.Context, tx pgx.Tx, aggregateID string) (int, error) {
	var version *int
	err := tx.QueryRow(ctx,
		`SELECT MAX(version) FROM events WHERE aggregate_id = $1`, aggregateID).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("querying current version: %w", err)
	}
	if version == nil {
		return 0, nil
	}
	return *version, nil
}

func (s *PostgresStore) insertEvent(ctx context.Context, tx pgx.Tx, e Event) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO events (event_id, aggregate_id, aggregate_type, event_type, version, correlation_id, payload, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.EventID, e.AggregateID, e.AggregateType, e.EventType, e.Version, e.CorrelationID, e.Payload, e.OccurredAt)
	if err != nil {
		return fmt.Errorf("inserting event: %w", err)
	}
	return nil
}
