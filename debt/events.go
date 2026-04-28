package debt

import "time"

const (
	EventTypeDebtRecorded = "DebtRecorded"
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
