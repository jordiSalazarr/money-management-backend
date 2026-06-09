package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	txprojections "github.com/jordisalazar/money-management/internal/payments/transaction/projections"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/projector"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// ProjectorOptions configures BuildProjector. AuroraURL is the Postgres
// connection string injected by the Lambda runtime; an empty AuroraURL
// falls back to the AURORA_URL env var so callers can pass {} for the
// common case.
type ProjectorOptions struct {
	AuroraURL string
}

// ProjectorBundle bundles the dispatcher and its query stores so the
// caller can defer Cleanup and (eventually) reuse Queries to mount GET
// handlers from the same pool.
type ProjectorBundle struct {
	Dispatcher *projector.Dispatcher
	Queries    queryStores
	Cleanup    func()
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
	txStore := txprojections.NewPostgresTransactionStore(pool)

	d := projector.NewDispatcher(slog.Default())
	registerDomainEvents(d)
	d.Subscribe(wprojections.NewWalletsProjector(q.wallet))
	d.Subscribe(wprojections.NewMovementsProjector(q.movement))
	d.Subscribe(txprojections.NewTransactionsProjector(txStore))
	d.Subscribe(dprojections.NewDebtsProjector(q.debt))
	d.Subscribe(rmprojections.NewRecurringMovementsProjector(q.recurring))

	log.Info("projector wired", "aurora_host_sanitised", sanitiseDSN(url))

	return &ProjectorBundle{
		Dispatcher: d,
		Queries:    q,
		Cleanup:    pool.Close,
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
