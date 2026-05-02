package projections

import (
	"context"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/recurringmovement"
	"github.com/jordisalazar/money-management/shared/eventstore"
)

type RecurringMovementView struct {
	ID          string    `json:"id"`
	WalletID    string    `json:"wallet_id"`
	UserID      string    `json:"user_id"`
	Type        string    `json:"type"`
	Amount      int       `json:"amount"`
	Currency    string    `json:"currency"`
	Frequency   string    `json:"frequency"`
	Reason      string    `json:"reason"`
	StartDate   time.Time `json:"start_date"`
	EndDate     *time.Time `json:"end_date"`
	NextDueDate time.Time `json:"next_due_date"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
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

	case recurringmovement.RecurringMovementExecuted:
		view, ok := p.cache[e.RecurringMovementID]
		if !ok {
			slog.Error("recurring movements projector: not in cache",
				"recurring_movement_id", e.RecurringMovementID)
			return
		}

		view.NextDueDate = advanceDate(e.ExecutedForDate, view.Frequency)
		if view.EndDate != nil && view.NextDueDate.After(*view.EndDate) {
			view.Active = false
		}

		if err := p.store.Upsert(ctx, view); err != nil {
			slog.Error("recurring movements projector: upsert failed",
				"recurring_movement_id", e.RecurringMovementID, "error", err)
		}

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
