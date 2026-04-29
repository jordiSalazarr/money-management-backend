package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry represents a pending outbox row.
type Entry struct {
	ID        string
	EventType string
	Payload   []byte
	CreatedAt time.Time
}

// Handler processes a single outbox entry. Returns an error if processing fails
// (the entry will be retried on the next poll).
type Handler func(ctx context.Context, entry Entry) error

// Poller reads unprocessed outbox entries and dispatches them to a handler.
type Poller struct {
	pool     *pgxpool.Pool
	handler  Handler
	interval time.Duration
}

// NewPoller creates an outbox poller that ticks at the given interval.
func NewPoller(pool *pgxpool.Pool, handler Handler, interval time.Duration) *Poller {
	return &Poller{
		pool:     pool,
		handler:  handler,
		interval: interval,
	}
}

// Start runs the poller in a blocking loop. Cancel the context to stop.
func (p *Poller) Start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("outbox poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *Poller) poll(ctx context.Context) {
	rows, err := p.pool.Query(ctx,
		`SELECT id, event_type, payload, created_at
		 FROM outbox
		 WHERE processed_at IS NULL
		 ORDER BY created_at
		 LIMIT 10`)
	if err != nil {
		slog.Error("outbox poller: query failed", "error", err)
		return
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			slog.Error("outbox poller: scan failed", "error", err)
			return
		}
		entries = append(entries, e)
	}

	for _, entry := range entries {
		if err := p.handler(ctx, entry); err != nil {
			slog.Error("outbox poller: handler failed",
				"outbox_id", entry.ID,
				"event_type", entry.EventType,
				"error", err,
			)
			continue
		}

		if _, err := p.pool.Exec(ctx,
			`UPDATE outbox SET processed_at = NOW() WHERE id = $1`, entry.ID); err != nil {
			slog.Error("outbox poller: mark processed failed",
				"outbox_id", entry.ID,
				"error", err,
			)
		}
	}
}
