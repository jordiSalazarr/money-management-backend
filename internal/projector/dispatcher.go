// Package projector decodes DynamoDB-flavoured Kinesis records into the
// typed domain events the existing in-process projectors subscribe to,
// and fans them out.
//
// The decode side is deliberately small: this package only knows the
// String and Number DDB attribute types, since the events table item
// shape only uses those. Generalising would just be churn — every
// attribute we read is already a string or a stringified number.
package projector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/aws/aws-lambda-go/events"

	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Dispatcher consumes Kinesis records and forwards typed domain events
// to a list of subscribers. Subscribers are the same eventstore.Subscriber
// instances the in-process projection projectors implement.
type Dispatcher struct {
	registry    map[string]func() any
	subscribers []eventstore.Subscriber
	log         *slog.Logger
}

func NewDispatcher(log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{
		registry: make(map[string]func() any),
		log:      log,
	}
}

// RegisterEvent maps a domain event_type string to a factory that yields
// a pointer to a zero value for JSON unmarshalling.
func (d *Dispatcher) RegisterEvent(eventType string, factory func() any) {
	d.registry[eventType] = factory
}

func (d *Dispatcher) Subscribe(s eventstore.Subscriber) {
	d.subscribers = append(d.subscribers, s)
}

// HandleBatch processes a batch of Kinesis records in arrival order.
//
// Per-shard concurrency is configured to 1 at the event-source mapping,
// so processing here can stay sequential. A non-nil error aborts the
// batch and Kinesis will retry the whole window — the projectors must
// be idempotent (deterministic IDs from meta.EventID — already true).
func (d *Dispatcher) HandleBatch(_ context.Context, recs []events.KinesisEventRecord) error {
	for _, r := range recs {
		if err := d.dispatchOne(r); err != nil {
			return fmt.Errorf("record %s: %w", r.EventID, err)
		}
	}
	return nil
}

func (d *Dispatcher) dispatchOne(r events.KinesisEventRecord) error {
	var raw kinesisDDBRecord
	if err := json.Unmarshal(r.Kinesis.Data, &raw); err != nil {
		return fmt.Errorf("decoding kinesis payload: %w", err)
	}

	switch raw.EventName {
	case "INSERT", "MODIFY":
		// Carry on.
	case "REMOVE":
		// Event-store items are immutable; a REMOVE means someone deleted
		// directly through the DDB console. Log loud, skip the record.
		d.log.Warn("projector: REMOVE record skipped — event store should be append-only",
			"event_id", r.EventID)
		return nil
	default:
		d.log.Warn("projector: unrecognised eventName skipped",
			"event_id", r.EventID, "eventName", raw.EventName)
		return nil
	}

	img := raw.Dynamodb.NewImage
	if img == nil {
		return errors.New("missing dynamodb.NewImage")
	}

	eventType, ok := img.string("event_type")
	if !ok {
		return errors.New("missing event_type attribute")
	}
	factory, registered := d.registry[eventType]
	if !registered {
		d.log.Debug("projector: unregistered event_type skipped",
			"event_id", r.EventID, "event_type", eventType)
		return nil
	}

	rawPayload, _ := img.string("payload")
	ptr := factory()
	if err := json.Unmarshal([]byte(rawPayload), ptr); err != nil {
		return fmt.Errorf("unmarshalling %s payload: %w", eventType, err)
	}
	val := reflect.ValueOf(ptr).Elem().Interface()

	eventID, _ := img.string("event_id")
	meta := eventstore.EventMetadata{EventID: eventID}

	for _, sub := range d.subscribers {
		sub.Handle(val, meta)
	}
	return nil
}

// kinesisDDBRecord is the JSON wire format produced by DynamoDB's
// "Kinesis data streams export" feature. Only the fields we read are
// modelled; the rest are dropped on the floor.
type kinesisDDBRecord struct {
	EventName string `json:"eventName"`
	Dynamodb  struct {
		NewImage ddbImage `json:"NewImage"`
	} `json:"dynamodb"`
}

// ddbImage is a thin wrapper around the attribute map. Each value is a
// JSON object whose single key is the attribute's type tag — "S" for
// string, "N" for number.
type ddbImage map[string]ddbAttr

type ddbAttr struct {
	S *string `json:"S,omitempty"`
	N *string `json:"N,omitempty"`
}

func (m ddbImage) string(name string) (string, bool) {
	v, ok := m[name]
	if !ok {
		return "", false
	}
	switch {
	case v.S != nil:
		return *v.S, true
	case v.N != nil:
		return *v.N, true
	default:
		return "", false
	}
}
