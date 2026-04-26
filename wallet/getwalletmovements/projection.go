package getwalletmovements

import (
	"sync"
	"time"

	"github.com/google/uuid"
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

type MovementsProjector struct {
	mu        sync.RWMutex
	movements map[string][]MovementView // keyed by wallet_id
}

func NewMovementsProjector() *MovementsProjector {
	return &MovementsProjector{
		movements: make(map[string][]MovementView),
	}
}

func (p *MovementsProjector) Handle(event any) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch e := event.(type) {
	case wallet.MoneyDeposited:
		p.movements[e.WalletID] = append(p.movements[e.WalletID], MovementView{
			ID:         uuid.New().String(),
			WalletID:   e.WalletID,
			Type:       "deposit",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   "",
			OccurredAt: e.DepositedAt,
		})
	case wallet.MoneyWithdrawn:
		p.movements[e.WalletID] = append(p.movements[e.WalletID], MovementView{
			ID:         uuid.New().String(),
			WalletID:   e.WalletID,
			Type:       "withdrawal",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   e.Category,
			OccurredAt: e.WithdrawnAt,
		})
	case transaction.TransactionCompleted:
		p.movements[e.FromWalletID] = append(p.movements[e.FromWalletID], MovementView{
			ID:         uuid.New().String(),
			WalletID:   e.FromWalletID,
			Type:       "transfer_out",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   "",
			OccurredAt: e.CompletedAt,
		})
		p.movements[e.ToWalletID] = append(p.movements[e.ToWalletID], MovementView{
			ID:         uuid.New().String(),
			WalletID:   e.ToWalletID,
			Type:       "transfer_in",
			Amount:     e.Amount,
			Currency:   e.Currency,
			Reason:     e.Reason,
			Category:   "",
			OccurredAt: e.CompletedAt,
		})
	}
}

func (p *MovementsProjector) ListByWalletID(walletID string) []MovementView {
	p.mu.RLock()
	defer p.mu.RUnlock()

	movements := p.movements[walletID]
	if movements == nil {
		return nil
	}

	result := make([]MovementView, len(movements))
	copy(result, movements)
	return result
}
