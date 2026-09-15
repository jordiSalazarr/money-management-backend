package app

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	pprojections "github.com/jordisalazar/money-management/internal/payments/processes/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
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
	// feed is nil for stores with no global log. DynamoDB is one: its read
	// side is a Kinesis export, not a subscription. Anything that needs a
	// feed must check.
	feed  eventstore.Feed
	codec *eventstore.Codec

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
	codec := eventcatalog.NewCodec()

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
			codec: codec,
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
		codec:        codec,
		queries:      buildAuroraQueryStores(pool),
		queriesReady: true,
	}, pool.Close, nil
}

func buildMemoryDeps(log logger.Logger) (deps, func(), error) {
	store := eventstore.NewMemoryStore()
	codec := eventcatalog.NewCodec()

	q := queryStores{
		wallet:    wprojections.NewMemoryWalletStore(),
		movement:  wprojections.NewMemoryMovementStore(),
		debt:      dprojections.NewMemoryDebtStore(),
		recurring: rmprojections.NewMemoryRecurringMovementStore(),
		processes: pprojections.NewMemoryProcessStore(),
	}

	// Projections are no longer fanned out by the store itself. The memory
	// store has a real global log now, so the same subscription the deployed
	// projector uses drives them here too — see RunAsync.
	log.Info("memory store wired (state is per-process, lost on exit)")
	return deps{
		mode:         StoreModeMemory,
		store:        store,
		feed:         store,
		codec:        codec,
		queries:      q,
		queriesReady: true,
	}, store.Close, nil
}
