package wallet

import "time"

const (
	EventTypeWalletAdded    = "WalletAdded"
	EventTypeMoneyDeposited = "MoneyDeposited"
	EventTypeMoneyWithdrawn = "MoneyWithdrawn"
	EventTypeWalletDisabled = "WalletDisabled"
	EventTypeMoneyRefunded  = "MoneyRefunded"
)

type WalletAdded struct {
	WalletID      string    `json:"wallet_id"`
	UserID        string    `json:"user_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	InitialAmount int       `json:"initial_amount"`
	Currency      string    `json:"currency"`
	Category      string    `json:"category"`
	CreatedAt     time.Time `json:"created_at"`
}

func (e WalletAdded) EventType() string { return EventTypeWalletAdded }

type MoneyDeposited struct {
	WalletID    string    `json:"wallet_id"`
	UserID      string    `json:"user_id"`
	Amount      int       `json:"amount"`
	Currency    string    `json:"currency"`
	Reason      string    `json:"reason"`
	Category    string    `json:"category"`
	DepositedAt time.Time `json:"deposited_at"`
	// IdempotencyKey is empty for HTTP-initiated deposits and carries the
	// source Kinesis event_id for reactor-initiated deposits. When non-empty
	// it lets the aggregate short-circuit on retries with ErrAlreadyProcessed.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func (e MoneyDeposited) EventType() string { return EventTypeMoneyDeposited }

type MoneyWithdrawn struct {
	WalletID    string    `json:"wallet_id"`
	UserID      string    `json:"user_id"`
	Amount      int       `json:"amount"`
	Currency    string    `json:"currency"`
	Reason      string    `json:"reason"`
	Category    string    `json:"category"`
	WithdrawnAt time.Time `json:"withdrawn_at"`
	// IdempotencyKey is empty for HTTP-initiated withdrawals and carries
	// the source Kinesis event_id for reactor-initiated withdrawals.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func (e MoneyWithdrawn) EventType() string { return EventTypeMoneyWithdrawn }

type WalletDisabled struct {
	WalletID   string    `json:"wallet_id"`
	UserID     string    `json:"user_id"`
	DisabledAt time.Time `json:"disabled_at"`
	Reason     string    `json:"reason"`
}

func (e WalletDisabled) EventType() string { return EventTypeWalletDisabled }

// MoneyRefunded is money returned to a wallet to undo a withdrawal that should
// not have stood — the compensating leg of a failed process.
//
// It is deliberately not a MoneyDeposited: a refund is not new money arriving,
// so it is not subject to the rules that govern new money arriving, and it
// carries no user-chosen category because a refund's category is not a choice.
type MoneyRefunded struct {
	WalletID   string    `json:"wallet_id"`
	UserID     string    `json:"user_id"`
	Amount     int       `json:"amount"`
	Currency   string    `json:"currency"`
	Reason     string    `json:"reason"`
	RefundedAt time.Time `json:"refunded_at"`
	// IdempotencyKey is always set for refunds — they are only ever issued by a
	// process, never by a user — and is what makes a redelivery a no-op.
	IdempotencyKey string `json:"idempotency_key"`
}

func (e MoneyRefunded) EventType() string { return EventTypeMoneyRefunded }
