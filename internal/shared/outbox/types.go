// Package outbox defines the Entry / Handler types that the
// debt-settlement and recurring-movement reactors are written against.
//
// Phase 1 used a Postgres-backed Poller in this package to deliver
// entries; that poller is gone now and the reactors are dormant until
// phase 3 wires them up against Kinesis. The types stay so the existing
// reactor code keeps compiling without a churn rewrite.
package outbox

import (
	"context"
	"time"
)

// Entry represents a single domain event handed to a reactor. Phase 1
// constructed these from a database row; phase 3 will construct them
// from a Kinesis record. The shape stays the same so reactors don't have
// to change.
type Entry struct {
	ID        string
	EventType string
	Payload   []byte
	CreatedAt time.Time
}

// Handler processes a single Entry. A non-nil error means the caller
// should retry; success means the entry is consumed.
type Handler func(ctx context.Context, entry Entry) error
