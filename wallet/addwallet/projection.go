package addwallet

import (
	"sync"
	"time"

	"github.com/jordisalazar/money-management/transaction"
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
}

type WalletsProjector struct {
	mu      sync.RWMutex
	wallets map[string]*WalletView
}

func NewWalletsProjector() *WalletsProjector {
	return &WalletsProjector{
		wallets: make(map[string]*WalletView),
	}
}

func (p *WalletsProjector) Handle(event any) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch e := event.(type) {
	case wallet.WalletAdded:
		p.wallets[e.WalletID] = &WalletView{
			ID:             e.WalletID,
			UserID:         e.UserID,
			Name:           e.Name,
			Description:    e.Description,
			CurrentBalance: e.InitialAmount,
			Currency:       e.Currency,
			Category:       e.Category,
			CreatedAt:      e.CreatedAt,
		}
	case wallet.MoneyDeposited:
		w, ok := p.wallets[e.WalletID]
		if !ok {
			return
		}
		w.CurrentBalance += e.Amount
		w.LastMovementAt = &e.DepositedAt
	case wallet.MoneyWithdrawn:
		w, ok := p.wallets[e.WalletID]
		if !ok {
			return
		}
		w.CurrentBalance -= e.Amount
		w.LastMovementAt = &e.WithdrawnAt
	case transaction.TransactionCompleted:
		if from, ok := p.wallets[e.FromWalletID]; ok {
			from.CurrentBalance -= e.Amount
			from.LastMovementAt = &e.CompletedAt
		}
		if to, ok := p.wallets[e.ToWalletID]; ok {
			to.CurrentBalance += e.Amount
			to.LastMovementAt = &e.CompletedAt
		}
	}
}

func (p *WalletsProjector) CountByUserID(userID string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	count := 0
	for _, w := range p.wallets {
		if w.UserID == userID {
			count++
		}
	}
	return count
}

func (p *WalletsProjector) ListByUserID(userID string) []WalletView {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []WalletView
	for _, w := range p.wallets {
		if w.UserID == userID {
			result = append(result, *w)
		}
	}
	return result
}
