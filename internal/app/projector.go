package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	pprojections "github.com/jordisalazar/money-management/internal/payments/processes/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/projector"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// ProjectorOptions configures BuildProjector. AuroraURL is the Postgres
// connection string injected by the Lambda runtime; an empty AuroraURL
// falls back to the AURORA_URL env var so callers can pass {} for the
// common case.
type ProjectorOptions struct {
	AuroraURL string
}

// ProjectorBundle bundles the dispatcher, the feed it consumes, and the
// query stores it writes.
type ProjectorBundle struct {
	Dispatcher *projector.Dispatcher
	Feed       eventstore.Feed
	Queries    queryStores
	Cleanup    func()
}

// Run subscribes and projects until ctx is cancelled.
//
// It starts from the beginning of the log on every boot. That is correct
// rather than merely tolerable: every projection derives its row ids from
// meta.EventID, so a replay rebuilds the same rows instead of duplicating
// them. Persisted checkpoints (ADR 0003) make this fast and let one
// projection be rebuilt alone; they are not what makes it safe.
func (b *ProjectorBundle) Run(ctx context.Context) error {
	return b.Feed.SubscribeAll(ctx, eventstore.Start, func(rec eventstore.Recorded) error {
		return b.Dispatcher.Handle(rec)
	})
}

// BuildProjector wires every read-model projection store on top of an
// Aurora pool, subscribes the in-process projectors, registers domain
// event factories on the dispatcher, and returns the bundle.
func BuildProjector(ctx context.Context, log logger.Logger, opts ProjectorOptions) (*ProjectorBundle, error) {
	url := opts.AuroraURL
	if url == "" {
		url = os.Getenv("AURORA_URL")
	}
	if url == "" {
		return nil, fmt.Errorf("AURORA_URL must be set to build the projector")
	}

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connecting to aurora: %w", err)
	}

	q := buildAuroraQueryStores(pool)

	mode, err := resolveStoreMode(Options{DefaultStore: StoreModeDynamoDB})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("resolving STORE: %w", err)
	}
	deps, storeCleanup, err := buildDeps(ctx, log, mode)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if deps.feed == nil {
		storeCleanup()
		pool.Close()
		return nil, fmt.Errorf("the projector needs a store with a global log to subscribe to; STORE=%s has none", mode)
	}

	d := projector.NewDispatcher(deps.codec, slog.Default())
	subscribeProjections(d, q)

	log.Info("projector wired", "aurora_host_sanitised", sanitiseDSN(url))

	return &ProjectorBundle{
		Dispatcher: d,
		Feed:       deps.feed,
		Queries:    q,
		Cleanup: func() {
			storeCleanup()
			pool.Close()
		},
	}, nil
}

// buildAuroraQueryStores assembles the four Postgres-backed projection
// stores that GET handlers and the voice slice consume. The transaction
// store isn't included because nothing reads it — only its own projector
// writes it.
func buildAuroraQueryStores(pool *pgxpool.Pool) queryStores {
	return queryStores{
		wallet:    wprojections.NewPostgresWalletStore(pool),
		movement:  wprojections.NewPostgresMovementStore(pool),
		debt:      dprojections.NewPostgresDebtStore(pool),
		recurring: rmprojections.NewPostgresRecurringMovementStore(pool),
		processes: pprojections.NewPostgresProcessStore(pool),
	}
}

// subscribeProjections is the one list of read models, used by every entry
// point so local and deployed builds cannot drift apart.
//
// Note all five share a dispatcher here. ADR 0003 gives each projection its
// own subscription and checkpoint so one can be rebuilt alone; that split
// lands with the real checkpoint store in phase 3, and this is the shape it
// grows out of.
func subscribeProjections(d *projector.Dispatcher, q queryStores) {
	d.Subscribe(wprojections.NewWalletsProjector(q.wallet))
	d.Subscribe(wprojections.NewMovementsProjector(q.movement))
	d.Subscribe(dprojections.NewDebtsProjector(q.debt))
	d.Subscribe(rmprojections.NewRecurringMovementsProjector(q.recurring))
	d.Subscribe(pprojections.NewProcessesProjector(q.processes))
}

// sanitiseDSN strips userinfo from a Postgres URL so the log line is safe
// to ship to log aggregators.
func sanitiseDSN(dsn string) string {
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '@' {
			return dsn[i+1:]
		}
	}
	return dsn
}
