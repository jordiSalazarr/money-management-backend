package projections

import (
	"context"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence/domain"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type RecurringMovementView struct {
	ID          string     `json:"id"`
	WalletID    string     `json:"wallet_id"`
	UserID      string     `json:"user_id"`
	Type        string     `json:"type"`
	Amount      int        `json:"amount"`
	Currency    string     `json:"currency"`
	Frequency   string     `json:"frequency"`
	Reason      string     `json:"reason"`
	StartDate   time.Time  `json:"start_date"`
	EndDate     *time.Time `json:"end_date"`
	NextDueDate time.Time  `json:"next_due_date"`
	Active      bool       `json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
}

type RecurringMovementFilter struct {
	WalletID string
	Active   *bool
}

type RecurringMovementStore interface {
	Upsert(ctx context.Context, view *RecurringMovementView) error
	List(ctx context.Context, filter RecurringMovementFilter) ([]RecurringMovementView, error)
}

type RecurringMovementsProjector struct {
	store RecurringMovementStore
	cache map[string]*RecurringMovementView
}

func NewRecurringMovementsProjector(store RecurringMovementStore) *RecurringMovementsProjector {
	return &RecurringMovementsProjector{
		store: store,
		cache: make(map[string]*RecurringMovementView),
	}
}

func advanceDate(from time.Time, frequency string) time.Time {
	switch frequency {
	case "DAILY":
		return from.AddDate(0, 0, 1)
	case "WEEKLY":
		return from.AddDate(0, 0, 7)
	case "MONTHLY":
		return from.AddDate(0, 1, 0)
	default:
		return from.AddDate(0, 0, 1)
	}
}

func (p *RecurringMovementsProjector) Handle(event any, _ eventstore.EventMetadata) {
	ctx := context.Background()

	switch e := event.(type) {
	case recurringmovement.RecurringMovementRegistered:
		var endDate *time.Time
		if !e.EndDate.IsZero() {
			endDate = &e.EndDate
		}

		view := &RecurringMovementView{
			ID:          e.RecurringMovementID,
			WalletID:    e.WalletID,
			UserID:      e.UserID,
			Type:        e.Type,
			Amount:      e.Amount,
			Currency:    e.Currency,
			Frequency:   e.Frequency,
			Reason:      e.Reason,
			StartDate:   e.StartDate,
			EndDate:     endDate,
			NextDueDate: e.StartDate,
			Active:      true,
			CreatedAt:   e.RegisteredAt,
		}
		p.cache[e.RecurringMovementID] = view
		if err := p.store.Upsert(ctx, view); err != nil {
			slog.Error("recurring movements projector: upsert failed",
				"recurring_movement_id", e.RecurringMovementID, "error", err)
		}

	// The schedule advances on either outcome of an occurrence. A skipped
	// month must still move NextDueDate on: leaving it where it was would make
	// the cron re-offer the same date forever, so one short balance would jam
	// the standing order permanently.
	case occurrence.MovementExecuted:
		p.advance(ctx, e.RecurringMovementID, e.ForDate)

	case occurrence.MovementSkipped:
		p.advance(ctx, e.RecurringMovementID, e.ForDate)

	case recurringmovement.RecurringMovementStopped:
		view, ok := p.cache[e.RecurringMovementID]
		if !ok {
			slog.Error("recurring movements projector: not in cache",
				"recurring_movement_id", e.RecurringMovementID)
			return
		}

		view.Active = false

		if err := p.store.Upsert(ctx, view); err != nil {
			slog.Error("recurring movements projector: upsert failed",
				"recurring_movement_id", e.RecurringMovementID, "error", err)
		}
	}
}

// advance moves the schedule past forDate. It is called for both outcomes of
// an occurrence.
//
// This projection reads events from two streams — the instruction's own, and
// its occurrences'. That is a read-side dependency on a process, which the
// one-way import rule in ADR 0002 permits: the rule exists to stop command
// handlers reaching into processes, and a projection issues no commands.
func (p *RecurringMovementsProjector) advance(ctx context.Context, recurringMovementID string, forDate time.Time) {
	view, ok := p.cache[recurringMovementID]
	if !ok {
		slog.Error("recurring movements projector: not in cache",
			"recurring_movement_id", recurringMovementID)
		return
	}

	view.NextDueDate = advanceDate(forDate, view.Frequency)
	if view.EndDate != nil && view.NextDueDate.After(*view.EndDate) {
		view.Active = false
	}

	if err := p.store.Upsert(ctx, view); err != nil {
		slog.Error("recurring movements projector: upsert failed",
			"recurring_movement_id", recurringMovementID, "error", err)
	}
}
