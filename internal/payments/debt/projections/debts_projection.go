package projections

import (
	"context"
	"fmt"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// DebtView is the read model for a debt.
type DebtView struct {
	ID        string    `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	OwnerID   string    `json:"owner_id" example:"user-123"`
	WalletID  string    `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Amount    int       `json:"amount" example:"1000"`
	Currency  string    `json:"currency" example:"EUR"`
	Direction string    `json:"direction" example:"IN"`
	Reason    string    `json:"reason" example:"Dinner split"`
	Status    string    `json:"status" example:"created"`
	CreatedAt time.Time `json:"created_at" example:"2026-04-28T12:00:00Z"`
}

type DebtFilter struct {
	OwnerID   string
	WalletID  string
	Direction *string
	Status    *string
}

type DebtStore interface {
	Upsert(ctx context.Context, view *DebtView) error
	// Get returns nil when the row does not exist.
	Get(ctx context.Context, id string) (*DebtView, error)
	List(ctx context.Context, filter DebtFilter) ([]DebtView, error)
}

type DebtsProjector struct {
	store DebtStore
}

func NewDebtsProjector(store DebtStore) *DebtsProjector {
	return &DebtsProjector{store: store}
}

// Handle applies one event to the debt read model.
//
// It covers the whole settlement lifecycle, not just its happy end. A debt
// being settled passes through "settling" while the reactor moves the money,
// and returns to standing if the wallet refuses — both are states a person is
// entitled to see, and showing nothing until it completes is what made the old
// behaviour feel instantaneous when it was not.
func (p *DebtsProjector) Handle(event any, _ eventstore.EventMetadata) error {
	ctx := context.Background()

	switch e := event.(type) {
	case debt.DebtRecorded:
		view := &DebtView{
			ID:        e.DebtID,
			OwnerID:   e.OwnerID,
			WalletID:  e.WalletID,
			Amount:    e.Amount,
			Currency:  e.Currency,
			Direction: e.Direction,
			Reason:    e.Reason,
			Status:    string(debt.DebtStatusCreated),
			CreatedAt: e.CreatedAt,
		}
		return p.save(ctx, view)

	case debt.DebtSettlementRequested:
		return p.setStatus(ctx, e.DebtID, string(debt.DebtStatusSettling))

	case debt.DebtSettled:
		return p.setStatus(ctx, e.DebtID, string(debt.DebtStatusSettled))

	case debt.DebtSettlementFailed:
		// The money never moved, so the debt still stands and can be settled
		// again later.
		return p.setStatus(ctx, e.DebtID, string(debt.DebtStatusCreated))
	}
	return nil
}

// setStatus is a read-modify-write against the cache. A miss means an event
// arrived for a debt whose DebtRecorded this projection never saw — the read
// model is already wrong, and letting the checkpoint advance past it would
// make that permanent.
func (p *DebtsProjector) setStatus(ctx context.Context, debtID, status string) error {
	view, err := p.store.Get(ctx, debtID)
	if err != nil {
		return fmt.Errorf("reading debt %s: %w", debtID, err)
	}
	if view == nil {
		return fmt.Errorf("debts projector: no debt %s — its DebtRecorded was never projected", debtID)
	}
	view.Status = status
	return p.save(ctx, view)
}

func (p *DebtsProjector) save(ctx context.Context, view *DebtView) error {
	if err := p.store.Upsert(ctx, view); err != nil {
		return fmt.Errorf("upserting debt %s: %w", view.ID, err)
	}
	return nil
}
