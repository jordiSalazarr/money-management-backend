package debt

import "time"

const (
	EventTypeDebtRecorded = "DebtRecorded"
	EventTypeDebtSettled  = "DebtSettled"
)

type DebtRecorded struct {
	DebtID    string    `json:"debt_id"`
	WalletID  string    `json:"wallet_id"`
	OwnerID   string    `json:"owner_id"`
	Amount    int       `json:"amount"`
	Currency  string    `json:"currency"`
	Direction string    `json:"direction"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

func (e DebtRecorded) EventType() string { return EventTypeDebtRecorded }

type DebtSettled struct {
	DebtID    string    `json:"debt_id"`
	SettledBy string    `json:"settled_by"`
	SettledAt time.Time `json:"settled_at"`
}

func (e DebtSettled) EventType() string { return EventTypeDebtSettled }
