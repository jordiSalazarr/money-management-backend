package executerecurringmovement

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jordisalazar/money-management/recurringmovement"
	"github.com/jordisalazar/money-management/shared/eventstore"
)

// Processor queries the recurring_movements_view for due items and executes them.
type Processor struct {
	pool     *pgxpool.Pool
	store    eventstore.Store
	interval time.Duration
}

// NewProcessor creates a recurring movement processor.
func NewProcessor(pool *pgxpool.Pool, store eventstore.Store, interval time.Duration) *Processor {
	return &Processor{
		pool:     pool,
		store:    store,
		interval: interval,
	}
}

// Start runs the processor in a blocking loop. Cancel the context to stop.
func (p *Processor) Start(ctx context.Context) {
	// Run once immediately on startup
	p.process(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("recurring movement processor stopped")
			return
		case <-ticker.C:
			p.process(ctx)
		}
	}
}

type dueItem struct {
	ID string
}

func (p *Processor) process(ctx context.Context) {
	rows, err := p.pool.Query(ctx,
		`SELECT id FROM recurring_movements_view
		 WHERE next_due_date <= CURRENT_DATE AND active = true`)
	if err != nil {
		slog.Error("recurring movement processor: query failed", "error", err)
		return
	}
	defer rows.Close()

	var items []dueItem
	for rows.Next() {
		var item dueItem
		if err := rows.Scan(&item.ID); err != nil {
			slog.Error("recurring movement processor: scan failed", "error", err)
			return
		}
		items = append(items, item)
	}

	if len(items) == 0 {
		return
	}

	slog.Info("recurring movement processor: found due items", "count", len(items))

	cmdHandler := NewCommandHandler(p.store)
	today := time.Now().UTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)

	for _, item := range items {
		err := cmdHandler.Handle(ctx, Command{
			RecurringMovementID: item.ID,
			ExecutedForDate:     today,
		})
		if err != nil {
			if errors.Is(err, recurringmovement.ErrAlreadyExecutedToday) || errors.Is(err, recurringmovement.ErrNotActive) {
				continue
			}
			slog.Error("recurring movement processor: execution failed",
				"recurring_movement_id", item.ID, "error", err)
			continue
		}
		slog.Info("recurring movement processor: executed",
			"recurring_movement_id", item.ID, "date", today.Format("2006-01-02"))
	}
}
