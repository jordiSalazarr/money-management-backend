package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jordisalazar/money-management/internal/payments/processes/debtsettlement"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer"
	"github.com/jordisalazar/money-management/internal/reactor"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// ReactorOptions configures BuildReactor. Nothing to configure today — the
// store is chosen by STORE and addressed by KURRENTDB_URL — but the parameter
// stays for parity with the other Build* entry points.
type ReactorOptions struct{}

// ReactorBundle is what cmd/reactor runs.
// Cleanup releases whatever buildDeps opened, and the field stays for parity
// with the projector / engine entry points.
type ReactorBundle struct {
	Router *reactor.Router
	// Persistent is the server-managed subscription the reactor consumes when
	// the store offers one. Nil falls back to a catch-up subscription.
	Persistent eventstore.PersistentFeed
	Feed       eventstore.Feed
	Cleanup    func()
}

// ReactorGroup is the persistent subscription group name. Changing it starts
// the reactor over from the beginning of the log, which is safe — every
// process step is idempotent — but will re-drive everything.
const ReactorGroup = "money-management-reactor"

// Run consumes until ctx is cancelled.
//
// It prefers a persistent subscription, which is what a reactor actually
// wants: the server owns the position, a transient fault redelivers that one
// message rather than stalling everything behind it, and a message that can
// never succeed is parked instead of blocking the queue forever.
//
// The catch-up fallback exists for stores with no such facility. It is
// strictly worse — one bad message stops the subscription — but it is better
// than not running at all.
func (b *ReactorBundle) Run(ctx context.Context) error {
	if b.Persistent != nil {
		return b.Router.Consume(ctx, b.Persistent, ReactorGroup)
	}
	return b.Feed.SubscribeAll(ctx, eventstore.Start, func(rec eventstore.Recorded) error {
		return b.Router.Handle(ctx, rec)
	})
}

// BuildReactor wires the event store and every process manager behind one
// router.
func BuildReactor(ctx context.Context, log logger.Logger, opts ReactorOptions) (*ReactorBundle, error) {
	mode, err := resolveStoreMode(Options{DefaultStore: StoreModeKurrentDB})
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
		log.Info("reactor wired", "store", mode)
	}

	persistent, _ := deps.store.(eventstore.PersistentFeed)

	return &ReactorBundle{
		Router:     router,
		Persistent: persistent,
		Feed:       deps.feed,
		Cleanup:    cleanup,
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
