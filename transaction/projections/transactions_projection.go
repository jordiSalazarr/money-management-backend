package projections

import (
	"context"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/shared/eventstore"
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

type TransactionStore interface {
	Insert(ctx context.Context, view *TransactionView) error
	UpdateStatus(ctx context.Context, transactionID string, status string, completedAt *time.Time) error
	ListByUserID(ctx context.Context, userID string) ([]TransactionView, error)
}

type TransactionsProjector struct {
	store TransactionStore
}

func NewTransactionsProjector(store TransactionStore) *TransactionsProjector {
	return &TransactionsProjector{store: store}
}

func (p *TransactionsProjector) Handle(event any, _ eventstore.EventMetadata) {
	ctx := context.Background()

	switch e := event.(type) {
	case transaction.TransactionCreated:
		view := &TransactionView{
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
		if err := p.store.Insert(ctx, view); err != nil {
			slog.Error("transactions projector: insert failed", "transaction_id", e.TransactionID, "error", err)
		}

	case transaction.TransactionCompleted:
		if err := p.store.UpdateStatus(ctx, e.TransactionID, "COMPLETED", &e.CompletedAt); err != nil {
			slog.Error("transactions projector: update status failed", "transaction_id", e.TransactionID, "error", err)
		}
	}
}
