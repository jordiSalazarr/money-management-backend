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
}

// NewRouter takes a logger for call-site symmetry with the projector, but does
// not keep one. Every outcome here is either a dispatch or a returned error;
// a log-and-continue path is exactly what this rewrite removed.
func NewRouter(codec *eventstore.Codec, _ *slog.Logger, handlers ...Handler) *Router {
	return &Router{codec: codec, handlers: handlers}
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
