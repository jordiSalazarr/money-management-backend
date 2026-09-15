// Package transfer is the transfer process: a person's request to move money
// between two of their wallets, and the state machine that carries it out.
//
// It is a process, not a receipt. Its intermediate states are observable and
// some of them are states in which no money moved at all. That is the whole
// reason it exists as an aggregate: KurrentDB has no multi-stream transaction,
// so the two wallet legs cannot be atomic, and something has to remember how
// far the attempt got.
package transfer

import "time"

// Statuses. These are snake_case because they surface verbatim in
// processes_view and in the GET /transfers/:id response.
const (
	StatusRequested = "requested"
	StatusDebited   = "debited"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusStuck     = "stuck"
)

type DomainEvent interface {
	EventType() string
}

type Aggregate struct {
	id            string
	status        string
	userID        string
	fromWalletID  string
	toWalletID    string
	amount        int
	currency      string
	reason        string
	failureReason string
	attempts      int
	version       int
	changes       []DomainEvent
}

func NewAggregate() *Aggregate { return &Aggregate{} }

// Request opens a transfer. The invariants checked here are the ones knowable
// without touching a wallet; whether the source can actually afford it is the
// wallet's business, checked when the reactor runs the debit.
func Request(transferID, userID, fromWalletID, toWalletID string, amount int, currency, reason string, now time.Time) (*Aggregate, error) {
	if userID == "" {
		return nil, ErrMissingUserID
	}
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if fromWalletID == toWalletID {
		return nil, ErrSameWallet
	}

	agg := &Aggregate{}
	agg.raise(TransferRequested{
		TransferID:   transferID,
		UserID:       userID,
		FromWalletID: fromWalletID,
		ToWalletID:   toWalletID,
		Amount:       amount,
		Currency:     currency,
		Reason:       reason,
		RequestedAt:  now,
	})
	return agg, nil
}

// MarkDebited records that the source wallet has been debited.
//
// Calling it on an already-debited transfer returns ErrAlreadyDebited rather
// than a generic conflict. That distinction is what lets the reactor tell "I
// already did this, ack and move on" from "something is genuinely wrong",
// which is the difference between draining a redelivery and livelocking on it.
func (a *Aggregate) MarkDebited(now time.Time) error {
	if a.IsTerminal() {
		return ErrAlreadyTerminal
	}
	if a.status == StatusDebited {
		return ErrAlreadyDebited
	}
	a.raise(TransferDebited{TransferID: a.id, DebitedAt: now})
	return nil
}

// Complete closes a transfer that got both legs through.
func (a *Aggregate) Complete(now time.Time) error {
	if a.IsTerminal() {
		return ErrAlreadyTerminal
	}
	if a.status != StatusDebited {
		return ErrNotDebited
	}
	a.raise(TransferCompleted{TransferID: a.id, CompletedAt: now})
	return nil
}

// Fail closes a transfer that moved no money — either rejected before the
// debit, or debited and then successfully refunded.
func (a *Aggregate) Fail(reason string, now time.Time) error {
	if a.IsTerminal() {
		return ErrAlreadyTerminal
	}
	a.raise(TransferFailed{TransferID: a.id, Reason: reason, FailedAt: now})
	return nil
}

// MarkStuck closes a transfer whose money left the source wallet and could not
// be returned.
//
// It requires that the debit actually happened. Reaching stuck without one
// would be a lie in the log, and would send someone looking for funds to
// recover that were never taken.
func (a *Aggregate) MarkStuck(reason string, now time.Time) error {
	if a.IsTerminal() {
		return ErrAlreadyTerminal
	}
	if a.status != StatusDebited {
		return ErrNotDebited
	}
	a.raise(TransferStuck{
		TransferID:       a.id,
		Reason:           reason,
		StrandedAmount:   a.amount,
		StrandedWalletID: a.fromWalletID,
		StuckAt:          now,
	})
	return nil
}

// RecordAttempt notes that the sweeper re-drove this transfer.
func (a *Aggregate) RecordAttempt(now time.Time) error {
	if a.IsTerminal() {
		return ErrAlreadyTerminal
	}
	a.raise(TransferRetryAttempted{
		TransferID:  a.id,
		Attempt:     a.attempts + 1,
		AttemptedAt: now,
	})
	return nil
}

// IsTerminal reports whether the transfer has reached its single outcome.
func (a *Aggregate) IsTerminal() bool {
	switch a.status {
	case StatusCompleted, StatusFailed, StatusStuck:
		return true
	}
	return false
}

// MoneyMoved reports whether the source wallet has been debited.
//
// This is what the sweeper consults when it gives up: no money moved means it
// is safe to write TransferFailed, money moved means the honest outcome is
// TransferStuck. The stream knows the answer, so nothing has to guess.
func (a *Aggregate) MoneyMoved() bool {
	switch a.status {
	case StatusDebited, StatusCompleted, StatusStuck:
		return true
	}
	return false
}

func (a *Aggregate) ID() string           { return a.id }
func (a *Aggregate) Status() string       { return a.status }
func (a *Aggregate) UserID() string       { return a.userID }
func (a *Aggregate) FromWalletID() string { return a.fromWalletID }
func (a *Aggregate) ToWalletID() string   { return a.toWalletID }
func (a *Aggregate) Amount() int          { return a.amount }
func (a *Aggregate) Currency() string     { return a.currency }
func (a *Aggregate) Reason() string       { return a.reason }

// FailureReason is the snake_case code on TransferFailed or TransferStuck,
// empty for any other state.
func (a *Aggregate) FailureReason() string  { return a.failureReason }
func (a *Aggregate) Attempts() int          { return a.attempts }
func (a *Aggregate) Version() int           { return a.version }
func (a *Aggregate) Changes() []DomainEvent { return a.changes }

func (a *Aggregate) raise(event DomainEvent) {
	a.Apply(event)
	a.changes = append(a.changes, event)
}

func (a *Aggregate) Apply(event any) {
	switch e := event.(type) {
	case TransferRequested:
		a.id = e.TransferID
		a.status = StatusRequested
		a.userID = e.UserID
		a.fromWalletID = e.FromWalletID
		a.toWalletID = e.ToWalletID
		a.amount = e.Amount
		a.currency = e.Currency
		a.reason = e.Reason
	case TransferDebited:
		a.status = StatusDebited
	case TransferCompleted:
		a.status = StatusCompleted
	case TransferFailed:
		a.status = StatusFailed
		a.failureReason = e.Reason
	case TransferStuck:
		a.status = StatusStuck
		a.failureReason = e.Reason
	case TransferRetryAttempted:
		a.attempts = e.Attempt
	}
	a.version++
}
