package debt

import "time"

const (
	EventTypeDebtRecorded            = "DebtRecorded"
	EventTypeDebtSettlementRequested = "DebtSettlementRequested"
	EventTypeDebtSettled             = "DebtSettled"
	EventTypeDebtSettlementFailed    = "DebtSettlementFailed"
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

// DebtSettlementRequested is a person saying they have squared up. It is an
// intention: the money has not moved yet, and the reactor may fail it.
//
// This event exists because DebtSettled used to be written here, before the
// wallet movement was attempted. When that movement was rejected — an ordinary
// insufficient balance was enough — the error was classified permanent and the
// record dropped, leaving a debt that read settled against a wallet that had
// paid nothing.
type DebtSettlementRequested struct {
	DebtID      string    `json:"debt_id"`
	RequestedBy string    `json:"requested_by"`
	RequestedAt time.Time `json:"requested_at"`
}

func (e DebtSettlementRequested) EventType() string { return EventTypeDebtSettlementRequested }

// DebtSettled means the money has actually moved. It is written last, once the
// wallet movement has gone through.
type DebtSettled struct {
	DebtID    string    `json:"debt_id"`
	SettledBy string    `json:"settled_by"`
	SettledAt time.Time `json:"settled_at"`
}

func (e DebtSettled) EventType() string { return EventTypeDebtSettled }

// DebtSettlementFailed means the wallet movement was refused, so the debt
// stands. The reason is the same snake_case code the API would have returned.
type DebtSettlementFailed struct {
	DebtID   string    `json:"debt_id"`
	Reason   string    `json:"reason"`
	FailedAt time.Time `json:"failed_at"`
}

func (e DebtSettlementFailed) EventType() string { return EventTypeDebtSettlementFailed }
