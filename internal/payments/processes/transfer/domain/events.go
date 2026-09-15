package transfer

import "time"

// The events of the transfer process. The shape is the one every process in
// this codebase uses: a request, zero or more steps, and exactly one terminal
// outcome. The past tense is reserved for facts that are already true — which
// is why the opening event is TransferRequested and not TransferMade.
//
// See ADR 0002.
const (
	EventTypeTransferRequested      = "TransferRequested"
	EventTypeTransferDebited        = "TransferDebited"
	EventTypeTransferCompleted      = "TransferCompleted"
	EventTypeTransferFailed         = "TransferFailed"
	EventTypeTransferStuck          = "TransferStuck"
	EventTypeTransferRetryAttempted = "TransferRetryAttempted"
)

// TransferRequested is a person asking for money to move. It is an intention,
// not an outcome: no money has moved when this is written.
type TransferRequested struct {
	TransferID   string    `json:"transfer_id"`
	UserID       string    `json:"user_id"`
	FromWalletID string    `json:"from_wallet_id"`
	ToWalletID   string    `json:"to_wallet_id"`
	Amount       int       `json:"amount"`
	Currency     string    `json:"currency"`
	Reason       string    `json:"reason"`
	RequestedAt  time.Time `json:"requested_at"`
}

func (e TransferRequested) EventType() string { return EventTypeTransferRequested }

// TransferDebited means the source wallet has been debited. From here on the
// money is out, so failing the transfer requires compensating first.
type TransferDebited struct {
	TransferID string    `json:"transfer_id"`
	DebitedAt  time.Time `json:"debited_at"`
}

func (e TransferDebited) EventType() string { return EventTypeTransferDebited }

// TransferCompleted means both legs succeeded. It is status-only and produces
// no movement rows: the wallet legs already emitted real MoneyWithdrawn and
// MoneyDeposited events, and deriving rows here too would show every transfer
// twice.
type TransferCompleted struct {
	TransferID  string    `json:"transfer_id"`
	CompletedAt time.Time `json:"completed_at"`
}

func (e TransferCompleted) EventType() string { return EventTypeTransferCompleted }

// TransferFailed means the transfer did not happen and no money moved — either
// it was rejected before the debit, or it was debited and successfully
// refunded. Either way the wallets are whole.
type TransferFailed struct {
	TransferID string `json:"transfer_id"`
	// Reason is a snake_case code, the same vocabulary as the API's error_code.
	Reason   string    `json:"reason"`
	FailedAt time.Time `json:"failed_at"`
}

func (e TransferFailed) EventType() string { return EventTypeTransferFailed }

// TransferStuck means money left the source wallet and could not be put back.
// It is the backstop for compensation failures that wallet.Refund does not
// already make impossible, and it exists so that this situation is written
// down rather than surviving only as a log line.
type TransferStuck struct {
	TransferID       string    `json:"transfer_id"`
	Reason           string    `json:"reason"`
	StrandedAmount   int       `json:"stranded_amount"`
	StrandedWalletID string    `json:"stranded_wallet_id"`
	StuckAt          time.Time `json:"stuck_at"`
}

func (e TransferStuck) EventType() string { return EventTypeTransferStuck }

// TransferRetryAttempted records that the sweeper re-drove this transfer. The
// count lives on the stream so it survives a sweeper restart.
type TransferRetryAttempted struct {
	TransferID  string    `json:"transfer_id"`
	Attempt     int       `json:"attempt"`
	AttemptedAt time.Time `json:"attempted_at"`
}

func (e TransferRetryAttempted) EventType() string { return EventTypeTransferRetryAttempted }
