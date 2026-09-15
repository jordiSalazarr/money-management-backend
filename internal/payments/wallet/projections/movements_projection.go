package projections

import (
	"context"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// MovementView is the read model for a wallet movement.
type MovementView struct {
	ID         string    `json:"id" example:"880e8400-e29b-41d4-a716-446655440000"`
	WalletID   string    `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Type       string    `json:"type" example:"deposit" enums:"deposit,withdrawal,refund"`
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

// MovementsProjector builds a wallet's ledger.
//
// It handles exactly the three events that change a balance, and nothing else.
// In particular it does NOT derive rows from transfer events: a completed
// transfer's two legs are already genuine MoneyWithdrawn and MoneyDeposited
// events on the two wallets, and deriving transfer_out/transfer_in rows here
// as well would show every transfer twice. That was the regression §4.8 of the
// migration plan warned about, and TestOneTransferYieldsExactlyTwoRows is what
// keeps it fixed.
//
// The consequence worth stating: this view is a pure ledger. Every row changes
// a balance, so its running total is always correct. Processes that moved no
// money — a skipped standing order, a transfer rejected before its debit —
// belong in processes_view, not here.
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
		p.insert(ctx, &MovementView{
			ID:         meta.EventID,
			WalletID:   e.WalletID,
			Type:       "deposit",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   e.Category,
			OccurredAt: e.DepositedAt,
		})

	case wallet.MoneyWithdrawn:
		p.insert(ctx, &MovementView{
			ID:         meta.EventID,
			WalletID:   e.WalletID,
			Type:       "withdrawal",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   e.Category,
			OccurredAt: e.WithdrawnAt,
		})

	case wallet.MoneyRefunded:
		// A refund is a real balance change, so it belongs in the ledger. Its
		// category is fixed rather than user-chosen — a refund is not a
		// spending decision.
		p.insert(ctx, &MovementView{
			ID:         meta.EventID,
			WalletID:   e.WalletID,
			Type:       "refund",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   "Refund",
			OccurredAt: e.RefundedAt,
		})
	}
}

// insert uses meta.EventID as the row id, so replaying the log rebuilds the
// same rows instead of duplicating them.
func (p *MovementsProjector) insert(ctx context.Context, view *MovementView) {
	if err := p.store.Insert(ctx, view); err != nil {
		slog.Error("movements projector: insert failed",
			"wallet_id", view.WalletID, "type", view.Type, "error", err)
	}
}
