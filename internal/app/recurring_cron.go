package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jordisalazar/money-management/internal/payments/recurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/executerecurringmovement"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// DueItemsSource returns the IDs of recurring-movement aggregates that
// are due for execution today. The cron uses Aurora's
// recurring_movements_view; tests use a fake.
type DueItemsSource interface {
	DueToday(ctx context.Context) ([]string, error)
}

// DispatchExecuteFunc dispatches an ExecuteRecurringMovement command.
// Real-life this wraps executerecurringmovement.CommandHandler.Handle;
// tests inject a fake to assert call shape.
type DispatchExecuteFunc func(ctx context.Context, id string, executedFor time.Time) error

// RecurringCronOptions configures BuildRecurringCron. AuroraURL and
// EventsTable fall back to env vars when empty.
type RecurringCronOptions struct {
	AuroraURL   string
	EventsTable string
}

// RecurringCronBundle is what cmd/recurring-cron exposes through its
// Lambda handler. Run() is idempotent enough to be invoked manually for
// catch-up runs without coordination — the recurring aggregate's
// ErrAlreadyExecutedToday guard prevents double-execution per day.
type RecurringCronBundle struct {
	log      *slog.Logger
	source   DueItemsSource
	dispatch DispatchExecuteFunc
	Cleanup  func()
}

// RecurringCronResponse is returned from Run() and surfaces as the
// Lambda invocation result, so a manual `aws lambda invoke` can see
// what happened.
type RecurringCronResponse struct {
	DispatchedCount int      `json:"dispatched_count"`
	SkippedCount    int      `json:"skipped_count"`
	Failures        []string `json:"failures,omitempty"`
}

// BuildRecurringCron wires Aurora + the DDB-backed event store + the
// existing executerecurringmovement command handler into a Bundle ready
// to Run.
func BuildRecurringCron(ctx context.Context, log logger.Logger, opts RecurringCronOptions) (*RecurringCronBundle, error) {
	auroraURL := opts.AuroraURL
	if auroraURL == "" {
		auroraURL = os.Getenv("AURORA_URL")
	}
	if auroraURL == "" {
		return nil, fmt.Errorf("AURORA_URL must be set to build the recurring cron")
	}

	eventsTable := opts.EventsTable
	if eventsTable == "" {
		eventsTable = os.Getenv("EVENTS_TABLE")
	}
	if eventsTable == "" {
		return nil, fmt.Errorf("EVENTS_TABLE must be set to build the recurring cron")
	}

	pool, err := pgxpool.New(ctx, auroraURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to aurora: %w", err)
	}

	store, err := eventstore.NewDynamoStore(ctx, eventsTable)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("building dynamo event store: %w", err)
	}
	registerDomainEvents(store)
	handler := executerecurringmovement.NewCommandHandler(store)

	if log != nil {
		log.Info("recurring cron wired", "events_table", eventsTable, "aurora_host_sanitised", sanitiseDSN(auroraURL))
	}

	return &RecurringCronBundle{
		log:    slog.Default(),
		source: &auroraDueItemsSource{pool: pool},
		dispatch: func(ctx context.Context, id string, executedFor time.Time) error {
			return handler.Handle(ctx, executerecurringmovement.Command{
				RecurringMovementID: id,
				ExecutedForDate:     executedFor,
			})
		},
		Cleanup: pool.Close,
	}, nil
}

// Run fetches every due item and dispatches an Execute command per row,
// classifying the per-row outcome:
//
//   - success           → DispatchedCount++
//   - ErrAlreadyExecutedToday or ErrNotActive → SkippedCount++ (expected)
//   - anything else     → Failures += (and Run continues; one bad row
//     should not stop the others)
func (b *RecurringCronBundle) Run(ctx context.Context) (RecurringCronResponse, error) {
	ids, err := b.source.DueToday(ctx)
	if err != nil {
		return RecurringCronResponse{}, fmt.Errorf("loading due items: %w", err)
	}
	if len(ids) == 0 {
		b.log.Info("recurring cron: no due items")
		return RecurringCronResponse{}, nil
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	resp := RecurringCronResponse{}
	for _, id := range ids {
		err := b.dispatch(ctx, id, today)
		switch {
		case err == nil:
			resp.DispatchedCount++
			b.log.Info("recurring cron: dispatched", "recurring_movement_id", id)
		case errors.Is(err, recurringmovement.ErrAlreadyExecutedToday),
			errors.Is(err, recurringmovement.ErrNotActive):
			resp.SkippedCount++
			b.log.Info("recurring cron: skipped", "recurring_movement_id", id, "reason", err)
		default:
			resp.Failures = append(resp.Failures, fmt.Sprintf("%s: %v", id, err))
			b.log.Error("recurring cron: dispatch failed", "recurring_movement_id", id, "err", err)
		}
	}
	return resp, nil
}

// auroraDueItemsSource is the production DueItemsSource. The query
// matches every row whose next_due_date has passed; the cron firing at
// 00:00 UTC the next day still catches yesterday's misses.
type auroraDueItemsSource struct {
	pool *pgxpool.Pool
}

func (s *auroraDueItemsSource) DueToday(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM recurring_movements_view
		 WHERE next_due_date <= CURRENT_DATE AND active = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
