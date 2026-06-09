package app

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jordisalazar/money-management/internal/auth"
	"github.com/jordisalazar/money-management/internal/payments/debt"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/transaction"
	"github.com/jordisalazar/money-management/internal/payments/wallet"
	"github.com/jordisalazar/money-management/internal/shared/database"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// deps bundles the per-mode infra that BuildEngine then composes into a
// gin.Engine. The shape is uniform across modes so engine.go doesn't have
// to switch on StoreMode again — only the constructors do.
type deps struct {
	mode      StoreMode
	store     eventstore.Store
	userStore auth.UserStorer

	// queriesReady is true only when read-model projection stores were
	// constructed alongside the event store. That happens for postgres
	// (Phase 1's only mode with projections); the dynamodb mode defers
	// to the projector Lambda and the memory mode skips them.
	queriesReady bool
	queries      queryStores

	// pool is non-nil only for postgres mode. The startBackgroundJobs and
	// Postgres-specific subsystems require it; other modes leave it nil.
	pool *pgxpool.Pool
}

// resolveStoreMode reads STORE and falls back to opts.DefaultStore. The
// caller's default is authoritative when STORE is unset so cmd/server
// keeps defaulting to postgres while cmd/lambda picks dynamodb.
func resolveStoreMode(opts Options) (StoreMode, error) {
	raw := os.Getenv("STORE")
	if raw == "" {
		if opts.DefaultStore == "" {
			return "", fmt.Errorf("STORE not set and no DefaultStore in Options")
		}
		return opts.DefaultStore, nil
	}
	switch StoreMode(raw) {
	case StoreModePostgres, StoreModeDynamoDB, StoreModeMemory:
		return StoreMode(raw), nil
	default:
		return "", fmt.Errorf("unknown STORE=%q (want postgres|dynamodb|memory)", raw)
	}
}

// buildDeps dispatches on store mode. Each branch owns its own infra
// lifecycle and returns a cleanup func; on error nothing leaks.
func buildDeps(ctx context.Context, log logger.Logger, mode StoreMode) (deps, func(), error) {
	switch mode {
	case StoreModePostgres:
		return buildPostgresDeps(ctx, log)
	case StoreModeDynamoDB:
		return buildDynamoDeps(ctx, log)
	case StoreModeMemory:
		return buildMemoryDeps(log)
	default:
		return deps{}, nil, fmt.Errorf("unsupported store mode: %s", mode)
	}
}

func buildPostgresDeps(ctx context.Context, log logger.Logger) (deps, func(), error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://money:money@localhost:5432/money_management?sslmode=disable"
	}

	if applied, err := database.Migrate(databaseURL); err != nil {
		return deps{}, nil, fmt.Errorf("running migrations: %w", err)
	} else if applied > 0 {
		log.Info("migrations applied", "count", applied)
	}

	pool, err := database.NewPool(ctx, databaseURL)
	if err != nil {
		return deps{}, nil, fmt.Errorf("connecting to database: %w", err)
	}
	cleanup := func() { pool.Close() }

	store := eventstore.NewPostgresStore(pool)
	registerDomainEvents(store)

	queries := buildQueryStores(pool)
	subscribeProjectors(store, queries, pool)

	if err := store.ReplayAll(ctx); err != nil {
		cleanup()
		return deps{}, nil, fmt.Errorf("replaying events: %w", err)
	}
	log.Info("projections hydrated from event store")

	return deps{
		mode:         StoreModePostgres,
		store:        store,
		userStore:    auth.NewPostgresUserStore(pool),
		queries:      queries,
		queriesReady: true,
		pool:         pool,
	}, cleanup, nil
}

func buildDynamoDeps(ctx context.Context, log logger.Logger) (deps, func(), error) {
	eventsTable := os.Getenv("EVENTS_TABLE")
	if eventsTable == "" {
		return deps{}, nil, fmt.Errorf("EVENTS_TABLE must be set when STORE=dynamodb")
	}
	usersTable := os.Getenv("USERS_TABLE")
	if usersTable == "" {
		return deps{}, nil, fmt.Errorf("USERS_TABLE must be set when STORE=dynamodb")
	}

	store, err := eventstore.NewDynamoStore(ctx, eventsTable)
	if err != nil {
		return deps{}, nil, fmt.Errorf("building dynamo event store: %w", err)
	}
	registerDomainEvents(store)

	userStore, err := auth.NewDynamoUserStore(ctx, usersTable)
	if err != nil {
		return deps{}, nil, fmt.Errorf("building dynamo user store: %w", err)
	}

	log.Info("dynamodb store wired", "events_table", eventsTable, "users_table", usersTable)

	return deps{
		mode:      StoreModeDynamoDB,
		store:     store,
		userStore: userStore,
	}, func() {}, nil
}

func buildMemoryDeps(log logger.Logger) (deps, func(), error) {
	store := eventstore.NewMemoryStore()
	registerDomainEvents(store)
	log.Info("memory store wired (state is per-process, lost on exit)")
	return deps{
		mode:      StoreModeMemory,
		store:     store,
		userStore: auth.NewMemoryUserStore(),
	}, func() {}, nil
}

func registerDomainEvents(store eventstore.Store) {
	store.RegisterEvent(wallet.EventTypeWalletAdded, func() any { return &wallet.WalletAdded{} })
	store.RegisterEvent(wallet.EventTypeMoneyDeposited, func() any { return &wallet.MoneyDeposited{} })
	store.RegisterEvent(wallet.EventTypeMoneyWithdrawn, func() any { return &wallet.MoneyWithdrawn{} })
	store.RegisterEvent(wallet.EventTypeWalletDisabled, func() any { return &wallet.WalletDisabled{} })
	store.RegisterEvent(transaction.EventTypeTransactionCompleted, func() any { return &transaction.TransactionCompleted{} })
	store.RegisterEvent(debt.EventTypeDebtRecorded, func() any { return &debt.DebtRecorded{} })
	store.RegisterEvent(debt.EventTypeDebtSettled, func() any { return &debt.DebtSettled{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementRegistered, func() any { return &recurringmovement.RecurringMovementRegistered{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementExecuted, func() any { return &recurringmovement.RecurringMovementExecuted{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementStopped, func() any { return &recurringmovement.RecurringMovementStopped{} })
}
