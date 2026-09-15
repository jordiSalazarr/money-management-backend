package eventstore

import (
	"context"
	"encoding/json"
	"time"
)

// StreamID names a stream. It renders as "<category>-<id>", which is what
// KurrentDB's category projections key off: naming a wallet stream
// "wallet-<uuid>" buys "$ce-wallet" for free.
type StreamID struct {
	Category string
	ID       string
}

func (s StreamID) String() string { return s.Category + "-" + s.ID }

// NoStream is the expectedVersion asserting that a stream does not yet exist.
// It is -1 so that a caller can derive it from an empty load without a special
// case — see ExpectedRevisionAfter.
const NoStream = -1

// Position is a point in the global ordered log. It is opaque and only
// meaningful to the store that produced it; projections persist it verbatim as
// their checkpoint.
type Position struct {
	Commit  uint64 `json:"commit"`
	Prepare uint64 `json:"prepare"`
}

// Start is the position before the first event in the log.
var Start = Position{}

// Event is an event on its way into the store. It carries no revision: stream
// position is the store's to assign, never the caller's to claim.
type Event struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	// SchemaVersion is the version of the payload shape, stamped by the codec
	// from the type's registration. It lives in metadata rather than in the
	// event type name so routing tables never change on a version bump.
	// See ADR 0001.
	SchemaVersion int             `json:"schema_version"`
	CorrelationID string          `json:"correlation_id"`
	Payload       json.RawMessage `json:"payload"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

// Recorded is an event read back out of the store: the event as written, plus
// where it landed. The payload is still raw — decoding is the Codec's job, and
// keeping it raw here is what lets upcasting live in exactly one place.
type Recorded struct {
	Event
	Stream   StreamID
	Revision int      // 0-based position within its own stream
	Position Position // position in the global log
}

// ExpectedRevisionAfter returns the expectedVersion to pass to Append after
// loading a stream: the revision of its last event, or NoStream if it is empty.
//
// It reads the revision the store assigned rather than counting the slice, so
// optimistic concurrency never depends on how many events a caller decoded.
func ExpectedRevisionAfter(loaded []Recorded) int {
	if len(loaded) == 0 {
		return NoStream
	}
	return loaded[len(loaded)-1].Revision
}

// Store is the write side: what command handlers and processes depend on.
type Store interface {
	// Append writes events to a stream, failing with ErrConcurrencyConflict if
	// the stream is not at expectedVersion. Pass NoStream to assert the stream
	// does not exist yet — this is what makes a deterministic stream name
	// (occurrence-<rmID>-<date>) usable as an idempotency key.
	Append(ctx context.Context, stream StreamID, events []Event, expectedVersion int) error

	// Load returns a whole stream in order, or ErrAggregateNotFound if it does
	// not exist.
	Load(ctx context.Context, stream StreamID) ([]Recorded, error)
}

// Feed is the read side: what the projector and reactor depend on.
type Feed interface {
	// SubscribeAll delivers every event in the global log after from, then
	// continues with new ones as they are appended, until the handler returns
	// an error or ctx is done. The handler's error is returned as-is, so a
	// caller can stop the subscription with a sentinel.
	SubscribeAll(ctx context.Context, from Position, handler func(Recorded) error) error
}

// EventMetadata is the envelope-level context a projection needs: EventID for
// deterministic row IDs, and position/stream for lag and provenance.
//
// It is deliberately narrower than Recorded — a projection has no business
// reaching for the raw payload, since the codec has already decoded it.
type EventMetadata struct {
	EventID       string
	Stream        StreamID
	Revision      int
	Position      Position
	CorrelationID string
	OccurredAt    time.Time
}

// Meta narrows a Recorded to what a projection is allowed to see.
func (r Recorded) Meta() EventMetadata {
	return EventMetadata{
		EventID:       r.EventID,
		Stream:        r.Stream,
		Revision:      r.Revision,
		Position:      r.Position,
		CorrelationID: r.CorrelationID,
		OccurredAt:    r.OccurredAt,
	}
}

// Subscriber is a projection. The projector decodes through the Codec and
// fans the decoded value out to every subscriber.
//
// A subscriber that does not recognise an event type simply ignores it: that
// is normal fan-out, not a fault. The fault case — a type no part of the
// system knows — is caught earlier, by the codec.
type Subscriber interface {
	Handle(event any, meta EventMetadata)
}
