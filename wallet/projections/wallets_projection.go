package projections

import (
	"context"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/wallet"
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
	ListByUserID(ctx context.Context, userID string) ([]WalletView, error)
}

type WalletsProjector struct {
	store WalletStore
	// cache keeps an in-memory copy so Handle can do read-modify-write
	// without a DB round-trip on every event.
	cache map[string]*WalletView
}

func NewWalletsProjector(store WalletStore) *WalletsProjector {
	return &WalletsProjector{
		store: store,
		cache: make(map[string]*WalletView),
	}
}

func (p *WalletsProjector) Handle(event any) {
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
		p.cache[e.WalletID] = view
		if err := p.store.Upsert(ctx, view); err != nil {
			slog.Error("wallets projector: upsert failed", "wallet_id", e.WalletID, "error", err)
		}

	case wallet.MoneyDeposited:
		w, ok := p.cache[e.WalletID]
		if !ok {
			return
		}
		w.CurrentBalance += e.Amount
		w.LastMovementAt = &e.DepositedAt
		if err := p.store.Upsert(ctx, w); err != nil {
			slog.Error("wallets projector: upsert failed", "wallet_id", e.WalletID, "error", err)
		}

	case wallet.MoneyWithdrawn:
		w, ok := p.cache[e.WalletID]
		if !ok {
			return
		}
		w.CurrentBalance -= e.Amount
		w.LastMovementAt = &e.WithdrawnAt
		if err := p.store.Upsert(ctx, w); err != nil {
			slog.Error("wallets projector: upsert failed", "wallet_id", e.WalletID, "error", err)
		}

	case wallet.WalletDisabled:
		w, ok := p.cache[e.WalletID]
		if !ok {
			return
		}
		w.Disabled = true
		if err := p.store.Upsert(ctx, w); err != nil {
			slog.Error("wallets projector: upsert failed", "wallet_id", e.WalletID, "error", err)
		}
	}
}
