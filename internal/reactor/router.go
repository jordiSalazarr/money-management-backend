// Package reactor decodes recorded events and dispatches them to the process
// managers that react to them.
//
// Compared to the projector's dispatcher, the difference is what a failure
// means. A projection that cannot keep up is behind; a reactor that cannot
// complete a step has left a process part-done, so its errors propagate to the
// caller, which retries. Permanent, rule-based rejections never reach here at
// all — each process classifies those itself and records a terminal outcome,
// which is precisely what the old router failed to do when it classified an
// error as permanent and then dropped the record.
package reactor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Handler is one process manager.
type Handler interface {
	Handle(ctx context.Context, event any, meta eventstore.EventMetadata) error
}

type Router struct {
	codec    *eventstore.Codec
	handlers []Handler
	log      *slog.Logger
}

func NewRouter(codec *eventstore.Codec, log *slog.Logger, handlers ...Handler) *Router {
	if log == nil {
		log = slog.Default()
	}
	return &Router{codec: codec, handlers: handlers, log: log}
}

// Handle decodes one recorded event and offers it to every process. Processes
// ignore what is not theirs.
func (r *Router) Handle(ctx context.Context, rec eventstore.Recorded) error {
	decoded, err := r.codec.Decode(rec)
	if err != nil {
		return fmt.Errorf("reactor: decoding %s at %s/%d: %w",
			rec.EventType, rec.Stream, rec.Revision, err)
	}

	meta := rec.Meta()
	for _, h := range r.handlers {
		if err := h.Handle(ctx, decoded, meta); err != nil {
			return fmt.Errorf("reactor: handling %s at %s/%d: %w",
				rec.EventType, rec.Stream, rec.Revision, err)
		}
	}
	return nil
}

// Consume runs the router against a persistent subscription.
//
// This is where the shape of a failure finally reaches the server. The old
// router had two outcomes — return nil, or return an error — and a permanent
// rejection took the first: the record was dropped with a log.Warn and the
// money was gone. It now has three, and none of them is "pretend it did not
// happen".
func (r *Router) Consume(ctx context.Context, feed eventstore.PersistentFeed, group string) error {
	return feed.SubscribePersistent(ctx, group, func(d eventstore.Delivery) eventstore.Disposition {
		err := r.Handle(ctx, d.Recorded)
		disposition := Classify(err, d.RetryCount)

		switch disposition {
		case eventstore.Retry:
			r.log.Warn("reactor: retrying",
				"event_type", d.EventType, "stream", d.Stream.String(),
				"retry_count", d.RetryCount, "err", err)
		case eventstore.Park:
			// Parking means a human has to look. Say so loudly and say why —
			// a parked message that nobody notices is the silent drop again,
			// just in a different queue.
			r.log.Error("reactor: parked — this needs attention",
				"event_type", d.EventType, "stream", d.Stream.String(),
				"revision", d.Revision, "retry_count", d.RetryCount, "err", err)
		}
		return disposition
	})
}

// maxRetries is where the consumer gives up on its own terms rather than
// waiting for the server's limit. A fault that has survived this many
// redeliveries is not transient, whatever it claimed to be.
const maxRetries = 10

// Classify decides what to tell the server about a message.
//
// The bias is towards Retry: redelivering a transient fault costs nothing,
// because every process step carries an idempotency key, whereas parking one
// that would have succeeded needs a person to notice and re-drive it.
//
// Park is for what cannot improve. An event type the codec does not know will
// not become known by being redelivered — and retrying it forever would block
// every process behind it, which is a worse failure than setting it aside.
func Classify(err error, retryCount int) eventstore.Disposition {
	switch {
	case err == nil:
		// Includes a rule-based rejection: the process recorded its terminal
		// outcome, so the work is done even though the answer was "no".
		return eventstore.Done

	case errors.Is(err, eventstore.ErrUnknownEventType),
		errors.Is(err, eventstore.ErrFutureSchemaVersion):
		return eventstore.Park

	case retryCount >= maxRetries:
		return eventstore.Park

	default:
		return eventstore.Retry
	}
}
