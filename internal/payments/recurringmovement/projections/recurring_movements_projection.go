package projections

import (
	"context"
	"fmt"
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
	// Get returns nil when the row does not exist.
	Get(ctx context.Context, id string) (*RecurringMovementView, error)
	List(ctx context.Context, filter RecurringMovementFilter) ([]RecurringMovementView, error)
}

type RecurringMovementsProjector struct {
	store RecurringMovementStore
}

func NewRecurringMovementsProjector(store RecurringMovementStore) *RecurringMovementsProjector {
	return &RecurringMovementsProjector{store: store}
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

func (p *RecurringMovementsProjector) Handle(event any, _ eventstore.EventMetadata) error {
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
		return p.save(ctx, view)

	// The schedule advances on either outcome of an occurrence. A skipped
	// month must still move NextDueDate on: leaving it where it was would make
	// the cron re-offer the same date forever, so one short balance would jam
	// the standing order permanently.
	case occurrence.MovementExecuted:
		return p.advance(ctx, e.RecurringMovementID, e.ForDate)

	case occurrence.MovementSkipped:
		return p.advance(ctx, e.RecurringMovementID, e.ForDate)

	case recurringmovement.RecurringMovementStopped:
		view, err := p.load(ctx, e.RecurringMovementID)
		if err != nil {
			return err
		}
		view.Active = false
		return p.save(ctx, view)
	}
	return nil
}

// advance moves the schedule past forDate. It is called for both outcomes of
// an occurrence.
//
// This projection reads events from two streams — the instruction's own, and
// its occurrences'. That is a read-side dependency on a process, which the
// one-way import rule in ADR 0002 permits: the rule exists to stop command
// handlers reaching into processes, and a projection issues no commands.
func (p *RecurringMovementsProjector) advance(ctx context.Context, recurringMovementID string, forDate time.Time) error {
	view, err := p.load(ctx, recurringMovementID)
	if err != nil {
		return err
	}

	view.NextDueDate = advanceDate(forDate, view.Frequency)
	if view.EndDate != nil && view.NextDueDate.After(*view.EndDate) {
		view.Active = false
	}
	return p.save(ctx, view)
}

func (p *RecurringMovementsProjector) save(ctx context.Context, view *RecurringMovementView) error {
	if err := p.store.Upsert(ctx, view); err != nil {
		return fmt.Errorf("upserting recurring movement %s: %w", view.ID, err)
	}
	return nil
}

func (p *RecurringMovementsProjector) load(ctx context.Context, id string) (*RecurringMovementView, error) {
	view, err := p.store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading recurring movement %s: %w", id, err)
	}
	if view == nil {
		return nil, fmt.Errorf("recurring movements projector: no instruction %s — its registration was never projected", id)
	}
	return view, nil
}
