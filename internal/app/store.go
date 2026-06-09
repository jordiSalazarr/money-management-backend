package app

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jordisalazar/money-management/internal/payments/debt"
	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/payments/transaction"
	txprojections "github.com/jordisalazar/money-management/internal/payments/transaction/projections"
	"github.com/jordisalazar/money-management/internal/payments/wallet"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// deps bundles the per-mode infra that BuildEngine composes into a
// gin.Engine. Phase 4 dropped the userStore field — Cognito owns users
// now; the backend only sees the validated sub claim from API GW.
type deps struct {
	mode  StoreMode
	store eventstore.Store

	// queriesReady is true only when read-model projection stores were
	// constructed alongside the event store. Memory mode wires them in
	// process; dynamodb mode wires them only when AURORA_URL is set.
	queriesReady bool
	queries      queryStores
}

// resolveStoreMode reads STORE and falls back to opts.DefaultStore. The
// caller's default is authoritative when STORE is unset.
func resolveStoreMode(opts Options) (StoreMode, error) {
	raw := os.Getenv("STORE")
	if raw == "" {
		if opts.DefaultStore == "" {
			return "", fmt.Errorf("STORE not set and no DefaultStore in Options")
		}
		return opts.DefaultStore, nil
	}
	switch StoreMode(raw) {
	case StoreModeDynamoDB, StoreModeMemory:
		return StoreMode(raw), nil
	default:
		return "", fmt.Errorf("unknown STORE=%q (want dynamodb|memory)", raw)
	}
}

// buildDeps dispatches on store mode. Each branch owns its own infra
// lifecycle and returns a cleanup func; on error nothing leaks.
func buildDeps(ctx context.Context, log logger.Logger, mode StoreMode) (deps, func(), error) {
	switch mode {
	case StoreModeDynamoDB:
		return buildDynamoDeps(ctx, log)
	case StoreModeMemory:
		return buildMemoryDeps(log)
	default:
		return deps{}, nil, fmt.Errorf("unsupported store mode: %s", mode)
	}
}

func buildDynamoDeps(ctx context.Context, log logger.Logger) (deps, func(), error) {
	eventsTable := os.Getenv("EVENTS_TABLE")
	if eventsTable == "" {
		return deps{}, nil, fmt.Errorf("EVENTS_TABLE must be set when STORE=dynamodb")
	}

	store, err := eventstore.NewDynamoStore(ctx, eventsTable)
	if err != nil {
		return deps{}, nil, fmt.Errorf("building dynamo event store: %w", err)
	}
	registerDomainEvents(store)

	log.Info("dynamodb store wired", "events_table", eventsTable)

	// AURORA_URL is optional. When set we additionally open a pgxpool and
	// build the Postgres-backed projection stores so GET handlers can
	// serve real data. When unset, the lambda runs POST-only and GETs 404.
	auroraURL := os.Getenv("AURORA_URL")
	if auroraURL == "" {
		log.Info("AURORA_URL unset — GET handlers stay off (post-only mode)")
		return deps{
			mode:  StoreModeDynamoDB,
			store: store,
		}, func() {}, nil
	}

	pool, err := pgxpool.New(ctx, auroraURL)
	if err != nil {
		return deps{}, nil, fmt.Errorf("connecting to aurora: %w", err)
	}
	log.Info("aurora projection stores wired", "aurora_host_sanitised", sanitiseDSN(auroraURL))

	return deps{
		mode:         StoreModeDynamoDB,
		store:        store,
		queries:      buildAuroraQueryStores(pool),
		queriesReady: true,
	}, pool.Close, nil
}

func buildMemoryDeps(log logger.Logger) (deps, func(), error) {
	store := eventstore.NewMemoryStore()
	registerDomainEvents(store)

	q := queryStores{
		wallet:    wprojections.NewMemoryWalletStore(),
		movement:  wprojections.NewMemoryMovementStore(),
		debt:      dprojections.NewMemoryDebtStore(),
		recurring: rmprojections.NewMemoryRecurringMovementStore(),
	}
	transactionStore := txprojections.NewMemoryTransactionStore()

	store.Subscribe(wprojections.NewWalletsProjector(q.wallet))
	store.Subscribe(wprojections.NewMovementsProjector(q.movement))
	store.Subscribe(txprojections.NewTransactionsProjector(transactionStore))
	store.Subscribe(dprojections.NewDebtsProjector(q.debt))
	store.Subscribe(rmprojections.NewRecurringMovementsProjector(q.recurring))

	log.Info("memory store wired (state is per-process, lost on exit)")
	return deps{
		mode:         StoreModeMemory,
		store:        store,
		queries:      q,
		queriesReady: true,
	}, func() {}, nil
}

// eventRegistrar is the slim surface both eventstore.Store and the
// projector.Dispatcher expose for receiving event-type factories.
// Keeping registerDomainEvents polymorphic over it means the canonical
// list of domain events lives in exactly one place.
type eventRegistrar interface {
	RegisterEvent(eventType string, factory func() any)
}

func registerDomainEvents(r eventRegistrar) {
	r.RegisterEvent(wallet.EventTypeWalletAdded, func() any { return &wallet.WalletAdded{} })
	r.RegisterEvent(wallet.EventTypeMoneyDeposited, func() any { return &wallet.MoneyDeposited{} })
	r.RegisterEvent(wallet.EventTypeMoneyWithdrawn, func() any { return &wallet.MoneyWithdrawn{} })
	r.RegisterEvent(wallet.EventTypeWalletDisabled, func() any { return &wallet.WalletDisabled{} })
	r.RegisterEvent(transaction.EventTypeTransactionCompleted, func() any { return &transaction.TransactionCompleted{} })
	r.RegisterEvent(debt.EventTypeDebtRecorded, func() any { return &debt.DebtRecorded{} })
	r.RegisterEvent(debt.EventTypeDebtSettled, func() any { return &debt.DebtSettled{} })
	r.RegisterEvent(recurringmovement.EventTypeRecurringMovementRegistered, func() any { return &recurringmovement.RecurringMovementRegistered{} })
	r.RegisterEvent(recurringmovement.EventTypeRecurringMovementExecuted, func() any { return &recurringmovement.RecurringMovementExecuted{} })
	r.RegisterEvent(recurringmovement.EventTypeRecurringMovementStopped, func() any { return &recurringmovement.RecurringMovementStopped{} })
}
