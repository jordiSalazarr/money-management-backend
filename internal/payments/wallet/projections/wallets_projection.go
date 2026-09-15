package projections

import (
	"context"
	"fmt"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// WalletView is the read model for a wallet.
type WalletView struct {
	ID             string     `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	UserID         string     `json:"user_id" example:"user-123"`
	Name           string     `json:"name" example:"Savings"`
	Description    string     `json:"description" example:"My savings wallet"`
	CurrentBalance int        `json:"current_balance" example:"15000"`
	Currency       string     `json:"currency" example:"EUR"`
	Category       string     `json:"category" example:"savings"`
	CreatedAt      time.Time  `json:"created_at" example:"2026-04-26T10:00:00Z"`
	LastMovementAt *time.Time `json:"last_movement_at" example:"2026-04-26T12:30:00Z"`
	Disabled       bool       `json:"disabled" example:"false"`
}

type WalletStore interface {
	Upsert(ctx context.Context, view *WalletView) error
	// Get returns nil when the row does not exist. Projections read-modify-write
	// through it, so they hold no state between events and can resume from a
	// checkpoint rather than only from the beginning of the log.
	Get(ctx context.Context, id string) (*WalletView, error)
	ListByUserID(ctx context.Context, userID string) ([]WalletView, error)
}

// WalletsProjector builds the wallet read model.
//
// It holds no state between events. It used to keep an in-memory cache to save
// a round trip, and that cache was incompatible with checkpointing: a
// projection resuming from a checkpoint starts with an empty cache and a
// populated table, so every wallet created before the checkpoint would miss.
// The cache only ever worked because the old pipeline always replayed the
// whole log. Reading through the store also makes the projector correct if it
// is ever run with more than one replica.
type WalletsProjector struct {
	store WalletStore
}

func NewWalletsProjector(store WalletStore) *WalletsProjector {
	return &WalletsProjector{store: store}
}

// Handle applies one event to the wallet read model.
//
// Every event that changes a balance must be here, or the projected balance
// drifts permanently from what the aggregate would compute — and the projected
// value is the one a person sees. MoneyRefunded was missing exactly that way:
// after a compensated transfer the wallet read short by the refunded amount,
// for good.
func (p *WalletsProjector) Handle(event any, _ eventstore.EventMetadata) error {
	ctx := context.Background()

	switch e := event.(type) {
	case wallet.WalletAdded:
		view := &WalletView{
			ID:             e.WalletID,
			UserID:         e.UserID,
			Name:           e.Name,
			Description:    e.Description,
			CurrentBalance: e.InitialAmount,
			Currency:       e.Currency,
			Category:       e.Category,
			CreatedAt:      e.CreatedAt,
		}
		return p.save(ctx, view)

	case wallet.MoneyDeposited:
		return p.adjust(ctx, e.WalletID, func(w *WalletView) {
			w.CurrentBalance += e.Amount
			w.LastMovementAt = &e.DepositedAt
		})

	case wallet.MoneyWithdrawn:
		return p.adjust(ctx, e.WalletID, func(w *WalletView) {
			w.CurrentBalance -= e.Amount
			w.LastMovementAt = &e.WithdrawnAt
		})

	case wallet.MoneyRefunded:
		// A refund puts money back. Omitting it here is what caused the drift
		// above; the movements ledger had the row but the balance did not.
		return p.adjust(ctx, e.WalletID, func(w *WalletView) {
			w.CurrentBalance += e.Amount
			w.LastMovementAt = &e.RefundedAt
		})

	case wallet.WalletDisabled:
		return p.adjust(ctx, e.WalletID, func(w *WalletView) {
			w.Disabled = true
		})
	}
	return nil
}

// adjust reads a wallet, applies a change, and writes it back.
//
// A missing row is an error, not a shrug: it means an event arrived for a
// wallet whose WalletAdded this projection never saw, so the read model is
// already inconsistent and advancing the checkpoint past it would make that
// permanent.
func (p *WalletsProjector) adjust(ctx context.Context, walletID string, change func(*WalletView)) error {
	w, err := p.store.Get(ctx, walletID)
	if err != nil {
		return fmt.Errorf("reading wallet %s: %w", walletID, err)
	}
	if w == nil {
		return fmt.Errorf("wallets projector: no wallet %s — its WalletAdded was never projected", walletID)
	}
	change(w)
	return p.save(ctx, w)
}

func (p *WalletsProjector) save(ctx context.Context, view *WalletView) error {
	if err := p.store.Upsert(ctx, view); err != nil {
		return fmt.Errorf("upserting wallet %s: %w", view.ID, err)
	}
	return nil
}
