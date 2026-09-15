package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jordisalazar/money-management/internal/payments/processes/debtsettlement"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer"
	"github.com/jordisalazar/money-management/internal/reactor"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// ReactorOptions configures BuildReactor. Both fields fall back to env
// vars when empty so the Lambda init path can pass {}.
type ReactorOptions struct {
	EventsTable string
}

// ReactorBundle is what cmd/reactor exposes through its Lambda handler.
// Cleanup is a no-op for the reactor today (the DDB store doesn't open
// long-lived connections), but the field stays for parity with the
// projector / engine entry points.
type ReactorBundle struct {
	Router  *reactor.Router
	Feed    eventstore.Feed
	Cleanup func()
}

// Run subscribes and reacts until ctx is cancelled.
//
// Returning an error from the handler stops the subscription, which is what a
// transient fault should do: the position is not advanced, so redelivery picks
// up exactly where it left off. Permanent, rule-based rejections never reach
// here — each process records its own terminal outcome and returns nil.
func (b *ReactorBundle) Run(ctx context.Context) error {
	return b.Feed.SubscribeAll(ctx, eventstore.Start, func(rec eventstore.Recorded) error {
		return b.Router.Handle(ctx, rec)
	})
}

// BuildReactor wires the event store and every process manager behind one
// router.
func BuildReactor(ctx context.Context, log logger.Logger, opts ReactorOptions) (*ReactorBundle, error) {
	eventsTable := opts.EventsTable
	if eventsTable == "" {
		eventsTable = os.Getenv("EVENTS_TABLE")
	}
	if eventsTable == "" {
		return nil, fmt.Errorf("EVENTS_TABLE must be set to build the reactor")
	}

	mode, err := resolveStoreMode(Options{DefaultStore: StoreModeDynamoDB})
	if err != nil {
		return nil, fmt.Errorf("resolving STORE: %w", err)
	}
	deps, cleanup, err := buildDeps(ctx, log, mode)
	if err != nil {
		return nil, err
	}
	if deps.feed == nil {
		cleanup()
		return nil, fmt.Errorf("the reactor needs a store with a global log to subscribe to; STORE=%s has none", mode)
	}

	router := buildReactorRouter(deps.store, deps.codec)

	if log != nil {
		log.Info("reactor wired", "events_table", eventsTable)
	}

	return &ReactorBundle{
		Router:  router,
		Feed:    deps.feed,
		Cleanup: cleanup,
	}, nil
}

// buildReactorRouter wires every process manager behind one router. Each
// ignores what is not its own, so adding a process is one more argument here
// and nothing else.
func buildReactorRouter(store eventstore.Store, codec *eventstore.Codec) *reactor.Router {
	return reactor.NewRouter(codec, slog.Default(),
		transfer.NewReactor(store, codec, nil),
		occurrence.NewReactor(store, codec, nil),
		debtsettlement.NewReactor(store, codec, nil),
	)
}
