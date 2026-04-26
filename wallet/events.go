package wallet

import "time"

const (
	EventTypeWalletAdded    = "WalletAdded"
	EventTypeMoneyDeposited = "MoneyDeposited"
	EventTypeMoneyWithdrawn = "MoneyWithdrawn"
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
	DepositedAt time.Time `json:"deposited_at"`
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
}

func (e MoneyWithdrawn) EventType() string { return EventTypeMoneyWithdrawn }
