package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jordisalazar/money-management/internal/payments/debt/slices/settledebt"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/slices/executerecurringmovement"
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
	Cleanup func()
}

// BuildReactor wires the DDB event store, the two typed reactor
// functions, and the router. Phase 3 only routes DebtSettled and
// RecurringMovementExecuted — extending to new event types is a tiny
// edit in internal/reactor/router.go plus adding the typed handler.
func BuildReactor(ctx context.Context, log logger.Logger, opts ReactorOptions) (*ReactorBundle, error) {
	eventsTable := opts.EventsTable
	if eventsTable == "" {
		eventsTable = os.Getenv("EVENTS_TABLE")
	}
	if eventsTable == "" {
		return nil, fmt.Errorf("EVENTS_TABLE must be set to build the reactor")
	}

	store, err := eventstore.NewDynamoStore(ctx, eventsTable)
	if err != nil {
		return nil, fmt.Errorf("building dynamo event store: %w", err)
	}
	registerDomainEvents(store)

	debtFn := settledebt.NewReactorHandler(store)
	recurringFn := executerecurringmovement.NewReactorHandler(store)

	router := reactor.NewRouter(slog.Default(), debtFn, recurringFn)

	if log != nil {
		log.Info("reactor wired", "events_table", eventsTable)
	}

	return &ReactorBundle{
		Router:  router,
		Cleanup: func() {},
	}, nil
}
