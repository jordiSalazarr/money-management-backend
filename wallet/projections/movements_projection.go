package projections

import (
	"context"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/transaction"
	"github.com/jordisalazar/money-management/wallet"
)

// MovementView is the read model for a wallet movement.
type MovementView struct {
	ID         string    `json:"id" example:"880e8400-e29b-41d4-a716-446655440000"`
	WalletID   string    `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Type       string    `json:"type" example:"deposit" enums:"deposit,withdrawal,transfer_in,transfer_out"`
	Amount     int       `json:"amount" example:"5000"`
	Currency   string    `json:"currency" example:"EUR"`
	Reason     string    `json:"reason" example:"Salary"`
	Category   string    `json:"category" example:"food"`
	OccurredAt time.Time `json:"occurred_at" example:"2026-04-26T12:30:00Z"`
}

type MovementStore interface {
	Insert(ctx context.Context, view *MovementView) error
	ListByWalletID(ctx context.Context, walletID string) ([]MovementView, error)
}

type MovementsProjector struct {
	store MovementStore
}

func NewMovementsProjector(store MovementStore) *MovementsProjector {
	return &MovementsProjector{store: store}
}

func (p *MovementsProjector) Handle(event any, meta eventstore.EventMetadata) {
	ctx := context.Background()

	switch e := event.(type) {
	case wallet.MoneyDeposited:
		view := &MovementView{
			ID:         meta.EventID,
			WalletID:   e.WalletID,
			Type:       "deposit",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   e.Category,
			OccurredAt: e.DepositedAt,
		}
		if err := p.store.Insert(ctx, view); err != nil {
			slog.Error("movements projector: insert failed", "wallet_id", e.WalletID, "error", err)
		}

	case wallet.MoneyWithdrawn:
		view := &MovementView{
			ID:         meta.EventID,
			WalletID:   e.WalletID,
			Type:       "withdrawal",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   e.Category,
			OccurredAt: e.WithdrawnAt,
		}
		if err := p.store.Insert(ctx, view); err != nil {
			slog.Error("movements projector: insert failed", "wallet_id", e.WalletID, "error", err)
		}

	case transaction.TransactionCompleted:
		out := &MovementView{
			ID:         meta.EventID + "-out",
			WalletID:   e.FromWalletID,
			Type:       "transfer_out",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   "",
			OccurredAt: e.CompletedAt,
		}
		if err := p.store.Insert(ctx, out); err != nil {
			slog.Error("movements projector: insert failed", "wallet_id", e.FromWalletID, "error", err)
		}

		in := &MovementView{
			ID:         meta.EventID + "-in",
			WalletID:   e.ToWalletID,
			Type:       "transfer_in",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   "",
			OccurredAt: e.CompletedAt,
		}
		if err := p.store.Insert(ctx, in); err != nil {
			slog.Error("movements projector: insert failed", "wallet_id", e.ToWalletID, "error", err)
		}
	}
}
