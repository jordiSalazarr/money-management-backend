package transfermoney

import (
	"sync"
	"time"

	"github.com/jordisalazar/money-management/transaction"
)

type TransactionView struct {
	TransactionID string     `json:"transaction_id"`
	UserID        string     `json:"user_id"`
	FromWalletID  string     `json:"from_wallet_id"`
	ToWalletID    string     `json:"to_wallet_id"`
	Amount        int        `json:"amount"`
	Currency      string     `json:"currency"`
	Reason        string     `json:"reason"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	CompletedAt   *time.Time `json:"completed_at"`
}

type TransactionsProjector struct {
	mu           sync.RWMutex
	transactions map[string]*TransactionView
}

func NewTransactionsProjector() *TransactionsProjector {
	return &TransactionsProjector{
		transactions: make(map[string]*TransactionView),
	}
}

func (p *TransactionsProjector) Handle(event any) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch e := event.(type) {
	case transaction.TransactionCreated:
		p.transactions[e.TransactionID] = &TransactionView{
			TransactionID: e.TransactionID,
			UserID:        e.UserID,
			FromWalletID:  e.FromWalletID,
			ToWalletID:    e.ToWalletID,
			Amount:        e.Amount,
			Currency:      e.Currency,
			Reason:        e.Reason,
			Status:        "CREATED",
			CreatedAt:     e.CreatedAt,
		}
	case transaction.TransactionCompleted:
		txn, ok := p.transactions[e.TransactionID]
		if !ok {
			return
		}
		txn.Status = "COMPLETED"
		txn.CompletedAt = &e.CompletedAt
	}
}

func (p *TransactionsProjector) ListByUserID(userID string) []TransactionView {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []TransactionView
	for _, txn := range p.transactions {
		if txn.UserID == userID {
			result = append(result, *txn)
		}
	}
	return result
}
