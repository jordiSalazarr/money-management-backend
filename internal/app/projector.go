package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	pprojections "github.com/jordisalazar/money-management/internal/payments/processes/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/projector"
	"github.com/jordisalazar/money-management/internal/shared/database"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// ProjectorOptions configures BuildProjector. AuroraURL is the Postgres
// connection string; an empty AuroraURL
// falls back to the AURORA_URL env var so callers can pass {} for the
// common case.
type ProjectorOptions struct {
	AuroraURL string
}

// ProjectorBundle is every projection, each with its own subscription.
type ProjectorBundle struct {
	Runners []*projector.Runner
	// Lag serves /metrics and /health/live. A projector with no HTTP surface
	// can only be probed on "is the process alive", which is precisely the
	// question that does not matter — one can be alive and twenty minutes
	// behind, serving a read model that disagrees with the log.
	Lag     *projector.LagReporter
	Queries queryStores
	Cleanup func()
}

// Run starts every projection and blocks until ctx is cancelled or one of
// them fails.
//
// A projection that fails stops the whole binary rather than being left behind
// quietly. Its checkpoint has not advanced, so a restart retries exactly the
// event it choked on — but a projector silently serving one stale read model
// is worse than one that is visibly down.
func (b *ProjectorBundle) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(b.Runners))
	for _, r := range b.Runners {
		go func() { errs <- r.Run(ctx) }()
	}

	for range b.Runners {
		if err := <-errs; err != nil && !errors.Is(err, context.Canceled) {
			cancel()
			return err
		}
	}
	return nil
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

	mode, err := resolveStoreMode(Options{DefaultStore: StoreModeKurrentDB})
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

	checkpoints := projector.NewPostgresCheckpoints(pool)
	transactor := database.NewPoolTransactor(pool)

	defs := projectionDefs(q)
	runners := make([]*projector.Runner, 0, len(defs))
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		runners = append(runners, projector.NewRunner(def, deps.feed, deps.codec, checkpoints, transactor, slog.Default()))
		names = append(names, def.Name)
	}

	head, ok := deps.store.(eventstore.Headed)
	if !ok {
		storeCleanup()
		pool.Close()
		return nil, fmt.Errorf("the projector needs a store that can report its log head, for lag")
	}

	log.Info("projector wired", "projections", len(runners), "postgres_host_sanitised", sanitiseDSN(url))

	return &ProjectorBundle{
		Runners: runners,
		Lag:     projector.NewLagReporter(checkpoints, head, names),
		Queries: q,
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
// buildAuroraQueryStores builds the read-side stores (bound to the pool) and
// the binders a projection uses to write through whichever transaction it is
// running in.
func buildAuroraQueryStores(pool *pgxpool.Pool) queryStores {
	return queryStores{
		wallet:    wprojections.NewPostgresWalletStore(pool),
		movement:  wprojections.NewPostgresMovementStore(pool),
		debt:      dprojections.NewPostgresDebtStore(pool),
		recurring: rmprojections.NewPostgresRecurringMovementStore(pool),
		processes: pprojections.NewPostgresProcessStore(pool),
		bindTx: &txBinders{
			wallet:   func(db database.DBTX) wprojections.WalletStore { return wprojections.NewPostgresWalletStore(db) },
			movement: func(db database.DBTX) wprojections.MovementStore { return wprojections.NewPostgresMovementStore(db) },
			debt:     func(db database.DBTX) dprojections.DebtStore { return dprojections.NewPostgresDebtStore(db) },
			recurring: func(db database.DBTX) rmprojections.RecurringMovementStore {
				return rmprojections.NewPostgresRecurringMovementStore(db)
			},
			processes: func(db database.DBTX) pprojections.ProcessStore { return pprojections.NewPostgresProcessStore(db) },
		},
	}
}

// projectionDefs is the one list of read models, used by every entry point so
// local and deployed builds cannot drift apart.
//
// Each gets its own subscription and its own checkpoint row (ADR 0003), which
// is what makes rebuilding one of them a local operation: truncate its table,
// delete its checkpoint, restart. With a shared position, adding a single
// upcaster would mean replaying all five.
//
// The Name is the checkpoint key. Changing one starts that projection over
// from the beginning of the log — which is the intended way to force a rebuild,
// but not something to do by accident.
//
// Build takes a DBTX because each event is applied inside a transaction that
// also carries the checkpoint. In memory mode the DBTX is nil and the stores
// ignore it.
func projectionDefs(q queryStores) []projector.Projection {
	return []projector.Projection{
		{
			Name:  "wallets_projector",
			Build: func(db database.DBTX) eventstore.Subscriber { return wprojections.NewWalletsProjector(q.walletFor(db)) },
		},
		{
			Name: "movements_projector",
			Build: func(db database.DBTX) eventstore.Subscriber {
				return wprojections.NewMovementsProjector(q.movementFor(db))
			},
		},
		{
			Name:  "debts_projector",
			Build: func(db database.DBTX) eventstore.Subscriber { return dprojections.NewDebtsProjector(q.debtFor(db)) },
		},
		{
			Name: "recurring_movements_projector",
			Build: func(db database.DBTX) eventstore.Subscriber {
				return rmprojections.NewRecurringMovementsProjector(q.recurringFor(db))
			},
		},
		{
			Name: "processes_projector",
			Build: func(db database.DBTX) eventstore.Subscriber {
				return pprojections.NewProcessesProjector(q.processesFor(db))
			},
		},
	}
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
