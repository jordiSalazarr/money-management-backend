package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	"github.com/jordisalazar/money-management/internal/payments/debt"
	"github.com/jordisalazar/money-management/internal/payments/debt/settledebt"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/executerecurringmovement"
	txprojections "github.com/jordisalazar/money-management/internal/payments/transaction/projections"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
	"github.com/jordisalazar/money-management/internal/shared/outbox"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// BuildEngine wires the full HTTP stack and returns the gin engine plus
// a cleanup func that callers should defer.
//
// The shape of the returned closure (nil-safe, always returnable) lets
// the Lambda entry point ignore it without special-casing — cmd/server
// just defers it like a Close().
func BuildEngine(ctx context.Context, log logger.Logger, opts Options) (*gin.Engine, func(), error) {
	mode, err := resolveStoreMode(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving STORE: %w", err)
	}
	log.Info("event store mode", "mode", string(mode))

	d, cleanup, err := buildDeps(ctx, log, mode)
	if err != nil {
		return nil, nil, err
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "dev-secret-do-not-use-in-production"
	}

	r := gin.New()
	r.Use(middleware.CORS())
	r.Use(middleware.Correlation())
	r.Use(middleware.Logging(log))
	r.Use(middleware.Auth(jwtSecret))

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	mountQueries := opts.MountQueryRoutes && d.queriesReady
	switch {
	case anthropicKey == "":
		log.Info("voice assistant disabled (ANTHROPIC_API_KEY not set)")
	case mountQueries:
		log.Info("voice assistant enabled")
	default:
		log.Info("voice assistant disabled (projection stores not available in this mode)")
	}

	effectiveOpts := opts
	effectiveOpts.MountQueryRoutes = mountQueries
	registerRoutes(r, d.store, d.userStore, jwtSecret, anthropicKey, d.queries, effectiveOpts)

	if opts.StartBackgroundJobs {
		if d.pool == nil {
			cleanup()
			return nil, nil, fmt.Errorf("StartBackgroundJobs=true requires postgres mode (no pool in %s)", mode)
		}
		startBackgroundJobs(ctx, log, d.pool, d.store)
	}

	return r, cleanup, nil
}

// buildQueryStores constructs every projection store. Both BuildEngine
// (HTTP) and the projector Lambda (Phase 2) will reuse this — the same
// types back read-model writes and read-model queries.
func buildQueryStores(pool *pgxpool.Pool) queryStores {
	return queryStores{
		wallet:    wprojections.NewPostgresWalletStore(pool),
		movement:  wprojections.NewPostgresMovementStore(pool),
		debt:      dprojections.NewPostgresDebtStore(pool),
		recurring: rmprojections.NewPostgresRecurringMovementStore(pool),
	}
}

// subscribeProjectors wires every projector to the event store so each
// Append triggers synchronous read-model writes. The transaction store
// is consumed only here (its only reader is its own projector).
func subscribeProjectors(store eventstore.Store, q queryStores, pool *pgxpool.Pool) {
	transactionStore := txprojections.NewPostgresTransactionStore(pool)

	store.Subscribe(wprojections.NewWalletsProjector(q.wallet))
	store.Subscribe(wprojections.NewMovementsProjector(q.movement))
	store.Subscribe(txprojections.NewTransactionsProjector(transactionStore))
	store.Subscribe(dprojections.NewDebtsProjector(q.debt))
	store.Subscribe(rmprojections.NewRecurringMovementsProjector(q.recurring))
}

// startBackgroundJobs boots the outbox poller and the recurring-movement
// processor. Both are long-lived goroutines that only make sense in the
// HTTP server — Lambda must pass StartBackgroundJobs: false.
func startBackgroundJobs(ctx context.Context, log logger.Logger, pool *pgxpool.Pool, store eventstore.Store) {
	debtReactor := settledebt.NewReactorHandler(store)
	recurringReactor := executerecurringmovement.NewReactorHandler(store)
	outboxRouter := func(ctx context.Context, entry outbox.Entry) error {
		switch entry.EventType {
		case debt.EventTypeDebtSettled:
			return debtReactor(ctx, entry)
		case recurringmovement.EventTypeRecurringMovementExecuted:
			return recurringReactor(ctx, entry)
		default:
			log.Warn("outbox: unknown event type", "event_type", entry.EventType)
			return nil
		}
	}
	poller := outbox.NewPoller(pool, outboxRouter, 2*time.Second)
	go poller.Start(ctx)
	log.Info("outbox poller started", "interval", "2s")

	rmProcessor := executerecurringmovement.NewProcessor(pool, store, 10*time.Second)
	go rmProcessor.Start(ctx)
	log.Info("recurring movement processor started", "interval", "10s")
}
