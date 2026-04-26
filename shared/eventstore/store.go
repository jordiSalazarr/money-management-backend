package eventstore

import (
	"context"
	"encoding/json"
	"time"
)

type Event struct {
	EventID       string          `json:"event_id"`
	AggregateID   string          `json:"aggregate_id"`
	AggregateType string          `json:"aggregate_type"`
	EventType     string          `json:"event_type"`
	Version       int             `json:"version"`
	CorrelationID string          `json:"correlation_id"`
	Payload       json.RawMessage `json:"payload"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

type AggregateEvents struct {
	AggregateID     string
	Events          []Event
	ExpectedVersion int
}

type Subscriber interface {
	Handle(event any)
}

type Store interface {
	Append(ctx context.Context, aggregateID string, events []Event, expectedVersion int) error
	AppendMulti(ctx context.Context, batches []AggregateEvents) error
	Load(ctx context.Context, aggregateID string) ([]Event, error)
	LoadAll(ctx context.Context) ([]Event, error)
	Subscribe(sub Subscriber)
	RegisterEvent(eventType string, factory func() any)
	ReplayAll(ctx context.Context) error
}
