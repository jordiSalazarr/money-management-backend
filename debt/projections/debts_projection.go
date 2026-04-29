package projections

import (
	"context"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/debt"
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
	List(ctx context.Context, filter DebtFilter) ([]DebtView, error)
}

type DebtsProjector struct {
	store DebtStore
	cache map[string]*DebtView
}

func NewDebtsProjector(store DebtStore) *DebtsProjector {
	return &DebtsProjector{
		store: store,
		cache: make(map[string]*DebtView),
	}
}

func (p *DebtsProjector) Handle(event any) {
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
			Status:    "created",
			CreatedAt: e.CreatedAt,
		}
		p.cache[e.DebtID] = view
		if err := p.store.Upsert(ctx, view); err != nil {
			slog.Error("debts projector: upsert failed", "debt_id", e.DebtID, "error", err)
		}
	case debt.DebtSettled:
		view, ok := p.cache[e.DebtID]
		if !ok {
			slog.Error("debts projector: debt not in cache", "debt_id", e.DebtID)
			return
		}
		view.Status = string(debt.DebtStatusSettled)
		if err := p.store.Upsert(ctx, view); err != nil {
			slog.Error("debts projector: upsert failed", "debt_id", e.DebtID, "error", err)
		}
	}
}
