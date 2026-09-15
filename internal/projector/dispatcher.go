// Package projector decodes recorded events and fans them out to the
// read-model projections.
//
// It used to decode DynamoDB-flavoured Kinesis records. That job disappeared
// with the port split: the store hands back a Recorded, the Codec turns it
// into a typed value, and this package is left with the one thing it was
// always really for — fan-out.
package projector

import (
	"fmt"
	"log/slog"

	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Dispatcher decodes a Recorded through the codec and forwards the typed
// value to every subscriber.
type Dispatcher struct {
	codec       *eventstore.Codec
	subscribers []eventstore.Subscriber
}

// NewDispatcher takes a logger for call-site symmetry with the reactor, but
// does not keep one: everything here either fans out or returns an error, and
// there is deliberately no third path where something is logged and dropped.
func NewDispatcher(codec *eventstore.Codec, _ *slog.Logger) *Dispatcher {
	return &Dispatcher{codec: codec}
}

func (d *Dispatcher) Subscribe(s eventstore.Subscriber) {
	d.subscribers = append(d.subscribers, s)
}

// Handle decodes one recorded event and fans it out.
//
// A decode failure is returned, not logged and skipped: an event type the
// codec has never heard of means the registry and the log disagree, and
// carrying on would silently skip history. A subscriber that does not
// recognise a type it *is* given simply ignores it — that is ordinary
// fan-out, and the two cases must not be confused.
func (d *Dispatcher) Handle(rec eventstore.Recorded) error {
	decoded, err := d.codec.Decode(rec)
	if err != nil {
		return fmt.Errorf("projector: decoding %s at %s/%d: %w",
			rec.EventType, rec.Stream, rec.Revision, err)
	}

	meta := rec.Meta()
	for _, sub := range d.subscribers {
		sub.Handle(decoded, meta)
	}
	return nil
}
