package transaction

import "time"

const (
	EventTypeTransactionCreated   = "TransactionCreated"
	EventTypeTransactionCompleted = "TransactionCompleted"
)

type TransactionCreated struct {
	TransactionID string    `json:"transaction_id"`
	UserID        string    `json:"user_id"`
	FromWalletID  string    `json:"from_wallet_id"`
	ToWalletID    string    `json:"to_wallet_id"`
	Amount        int       `json:"amount"`
	Currency      string    `json:"currency"`
	Reason        string    `json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
}

func (e TransactionCreated) EventType() string { return EventTypeTransactionCreated }

type TransactionCompleted struct {
	TransactionID string    `json:"transaction_id"`
	UserID        string    `json:"user_id"`
	FromWalletID  string    `json:"from_wallet_id"`
	ToWalletID    string    `json:"to_wallet_id"`
	Amount        int       `json:"amount"`
	Currency      string    `json:"currency"`
	Reason        string    `json:"reason"`
	CompletedAt   time.Time `json:"completed_at"`
}

func (e TransactionCompleted) EventType() string { return EventTypeTransactionCompleted }
