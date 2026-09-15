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

// Headed is implemented by stores that can report where the end of the global
// log is.
//
// Lag — head minus a stored checkpoint — is the only meaningful thing a
// projector's liveness probe can report, and a catch-up reader needs it to
// tell "caught up" from "waiting".
type Headed interface {
	Head(ctx context.Context) (Position, error)
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
// hands the decoded value to it.
//
// A subscriber that does not recognise an event type simply ignores it and
// returns nil: that is normal fan-out, not a fault. The fault case — a type no
// part of the system knows — is caught earlier, by the codec.
//
// Handle returns an error because the checkpoint commits in the same
// transaction as the rows. A projection that could not write has to be able to
// abort that transaction; otherwise its position would advance past work it
// never did, and the gap would be invisible and permanent.
type Subscriber interface {
	Handle(event any, meta EventMetadata) error
}

// Disposition is what a consumer tells the server about a message it was
// handed. It exists because a reactor's failures are not all alike, and
// treating them alike is what lost money before: a permanent rejection dropped
// with a log line looked exactly like a transient one that would be retried.
type Disposition int

const (
	// Done: handled. Do not redeliver.
	//
	// This covers a rule-based rejection too. The process has already recorded
	// its terminal outcome, so the work IS done — there is simply nothing more
	// to try.
	Done Disposition = iota

	// Retry: a transient fault. Redeliver; idempotency keys make that free.
	Retry

	// Park: this will never succeed. Move it aside rather than let it block
	// the subscription forever, and leave it where a person can find it.
	Park
)

func (d Disposition) String() string {
	switch d {
	case Done:
		return "done"
	case Retry:
		return "retry"
	case Park:
		return "park"
	default:
		return "unknown"
	}
}

// Delivery is one message from a persistent subscription.
type Delivery struct {
	Recorded
	// RetryCount is how many times this message has already been redelivered.
	// A consumer can use it to give up on its own terms rather than relying
	// solely on the server's limit.
	RetryCount int
}

// PersistentFeed is a server-managed subscription: the server owns the
// position, redelivers what is not acknowledged, and parks what keeps failing.
//
// The projector deliberately does not use this. Projections need total
// ordering and a checkpoint we control so one can be rebuilt alone; reactors
// need per-message retry and a dead-letter path, and are idempotent by
// construction. Different needs, different subscription.
type PersistentFeed interface {
	// SubscribePersistent joins the named group, creating it if it does not
	// exist, and consumes until ctx is done.
	SubscribePersistent(ctx context.Context, group string, handler func(Delivery) Disposition) error
}
