package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrUnknownEventType means the codec has never heard of this event type.
	// It is a fault, not a routing decision: an event type that IS registered
	// but is simply not handled by a given projector or reactor is that
	// consumer's business to ignore. See ADR 0001.
	ErrUnknownEventType = errors.New("event type not registered with the codec")

	// ErrFutureSchemaVersion means an event was written by a newer deployment
	// than the one reading it. Decoding it as the older shape would silently
	// drop whatever the newer version added.
	ErrFutureSchemaVersion = errors.New("event schema version is newer than this build understands")
)

// DomainEvent is the one thing the codec needs from a domain event. Domain
// packages declare their own identical interface and never import this one —
// Go's structural typing means their values satisfy this anyway.
type DomainEvent interface {
	EventType() string
}

// Codec is the single place a raw payload becomes a typed value, and the
// single place a typed value becomes a payload. It owns the event type
// registry and the upcaster chains together, because a type's current version
// and the steps needed to reach it are one fact, not two.
//
// Both sides of the system use the same Codec: aggregate rehydration on the
// write side, the projector and reactor on the read side. That is the point —
// seven hand-written decode switches is how one consumer ends up seeing a
// different shape from another.
type Codec struct {
	mu        sync.RWMutex
	current   map[string]int
	factories map[string]func() any
	upcasters map[upcastStep]func(json.RawMessage) (json.RawMessage, error)
}

type upcastStep struct {
	eventType string
	from      int
}

func NewCodec() *Codec {
	return &Codec{
		current:   make(map[string]int),
		factories: make(map[string]func() any),
		upcasters: make(map[upcastStep]func(json.RawMessage) (json.RawMessage, error)),
	}
}

// Register declares an event type, the version its current shape is at, and a
// factory returning a pointer to that shape. The version given here is what
// Encode stamps into metadata, so write and read agree by construction.
func (c *Codec) Register(eventType string, currentVersion int, factory func() any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current[eventType] = currentVersion
	c.factories[eventType] = factory
}

// Upcast registers one or more single-step upcasters. Steps chain: v1→v2→v3.
// A step with no registered upcaster is an identity, which is what makes it
// safe to bump the version on an additive change that needs no translation.
func (c *Codec) Upcast(steps ...Upcaster) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range steps {
		c.upcasters[upcastStep{s.eventType, s.from}] = s.apply
	}
}

// Encode marshals a domain event into an envelope, stamping the schema version
// registered for its type.
func (c *Codec) Encode(e DomainEvent, correlationID string, occurredAt time.Time) (Event, error) {
	eventType := e.EventType()

	c.mu.RLock()
	version, known := c.current[eventType]
	c.mu.RUnlock()
	if !known {
		return Event{}, fmt.Errorf("%w: %s", ErrUnknownEventType, eventType)
	}

	payload, err := json.Marshal(e)
	if err != nil {
		return Event{}, fmt.Errorf("marshalling %s: %w", eventType, err)
	}

	return Event{
		EventID:       uuid.NewString(),
		EventType:     eventType,
		SchemaVersion: version,
		CorrelationID: correlationID,
		Payload:       payload,
		OccurredAt:    occurredAt.UTC(),
	}, nil
}

// EncodeAll is Encode over a slice, preserving order.
func (c *Codec) EncodeAll(events []DomainEvent, correlationID string, occurredAt time.Time) ([]Event, error) {
	out := make([]Event, 0, len(events))
	for _, e := range events {
		env, err := c.Encode(e, correlationID, occurredAt)
		if err != nil {
			return nil, err
		}
		out = append(out, env)
	}
	return out, nil
}

// Decode runs the upcaster chain from the event's stored version up to the
// current one, then unmarshals into the registered shape. It returns a value,
// not a pointer, because Apply() and the projections switch on value types.
func (c *Codec) Decode(r Recorded) (any, error) {
	c.mu.RLock()
	current, known := c.current[r.EventType]
	factory := c.factories[r.EventType]
	c.mu.RUnlock()

	if !known {
		return nil, fmt.Errorf("%w: %s (stream %s, revision %d)",
			ErrUnknownEventType, r.EventType, r.Stream, r.Revision)
	}

	// A zero version means the event predates versioning. Nothing in a fresh
	// store should carry one, but treating it as v1 is the only sane reading.
	from := r.SchemaVersion
	if from == 0 {
		from = 1
	}
	if from > current {
		return nil, fmt.Errorf("%w: %s is at v%d, this build knows v%d",
			ErrFutureSchemaVersion, r.EventType, from, current)
	}

	payload := r.Payload
	for v := from; v < current; v++ {
		c.mu.RLock()
		step, ok := c.upcasters[upcastStep{r.EventType, v}]
		c.mu.RUnlock()
		if !ok {
			continue // identity: the shape changed additively, no translation needed
		}
		next, err := step(payload)
		if err != nil {
			return nil, fmt.Errorf("upcasting %s v%d->v%d: %w", r.EventType, v, v+1, err)
		}
		payload = next
	}

	ptr := factory()
	if err := json.Unmarshal(payload, ptr); err != nil {
		return nil, fmt.Errorf("unmarshalling %s v%d: %w", r.EventType, current, err)
	}
	return reflect.ValueOf(ptr).Elem().Interface(), nil
}

// DecodeAll is Decode over a slice, preserving order. This is the aggregate
// rehydration path.
func (c *Codec) DecodeAll(recorded []Recorded) ([]any, error) {
	out := make([]any, 0, len(recorded))
	for _, r := range recorded {
		v, err := c.Decode(r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Knows reports whether an event type is registered. Consumers that legitimately
// see types they do not handle use this to tell "not mine" from "not known".
func (c *Codec) Knows(eventType string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.current[eventType]
	return ok
}

// AppendChanges encodes an aggregate's uncommitted events and appends them to
// its stream at the expected revision. Every command handler ends with this.
//
// The type parameter lets it take a domain package's own []wallet.DomainEvent
// directly, so domain code never has to know this package exists.
func AppendChanges[T DomainEvent](
	ctx context.Context,
	store Store,
	codec *Codec,
	stream StreamID,
	expectedVersion int,
	changes []T,
	correlationID string,
	occurredAt time.Time,
) error {
	if len(changes) == 0 {
		return nil
	}
	envelopes := make([]Event, 0, len(changes))
	for _, c := range changes {
		env, err := codec.Encode(c, correlationID, occurredAt)
		if err != nil {
			return err
		}
		envelopes = append(envelopes, env)
	}
	return store.Append(ctx, stream, envelopes, expectedVersion)
}

// Rehydrate loads a stream, decodes it through the codec, and replays it into
// apply. It returns the raw stream so the caller can derive the expected
// revision from it — never from the count of decoded events.
//
// A stream that does not exist yet is not an error here: it yields no events
// and NoStream, which is what a creation command needs.
func Rehydrate(
	ctx context.Context,
	store Store,
	codec *Codec,
	stream StreamID,
	apply func(any),
) (expectedVersion int, err error) {
	recorded, err := store.Load(ctx, stream)
	if err != nil && !errors.Is(err, ErrAggregateNotFound) {
		return NoStream, err
	}
	events, err := codec.DecodeAll(recorded)
	if err != nil {
		return NoStream, err
	}
	for _, e := range events {
		apply(e)
	}
	return ExpectedRevisionAfter(recorded), nil
}
