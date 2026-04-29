package eventstore

import (
	"encoding/json"
	"time"
)

// OutboxEntry represents a pending side-effect to be processed by the outbox poller.
type OutboxEntry struct {
	ID        string          `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}
