// Package occurrence is one firing of a recurring movement, for one date.
//
// A recurring movement fires forever, so its own stream holds only the
// instruction — registered, stopped — and stays bounded. Each firing gets a
// short-lived stream of its own, named from the instruction and the date. That
// keeps the instruction stream small, removes contention between occurrences
// catching up after an outage, and makes the stream name a natural idempotency
// key. See ADR 0002.
package occurrence

import "time"

const (
	StatusDue      = "due"
	StatusExecuted = "executed"
	StatusSkipped  = "skipped"
)

type DomainEvent interface {
	EventType() string
}

type Aggregate struct {
	id                  string
	recurringMovementID string
	walletID            string
	userID              string
	typ                 string
	amount              int
	currency            string
	reason              string
	forDate             time.Time
	status              string
	skipReason          string
	attempts            int
	version             int
	changes             []DomainEvent
}

func NewAggregate() *Aggregate { return &Aggregate{} }

// Due opens an occurrence.
func Due(occurrenceID, recurringMovementID, walletID, userID, typ string, amount int, currency, reason string, forDate, now time.Time) (*Aggregate, error) {
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if typ != "deposit" && typ != "withdrawal" {
		return nil, ErrInvalidType
	}

	agg := &Aggregate{}
	agg.raise(MovementDue{
		OccurrenceID:        occurrenceID,
		RecurringMovementID: recurringMovementID,
		WalletID:            walletID,
		UserID:              userID,
		Type:                typ,
		Amount:              amount,
		Currency:            currency,
		Reason:              reason,
		ForDate:             forDate,
		DueAt:               now,
	})
	return agg, nil
}

// Execute closes the occurrence once the money has moved.
func (a *Aggregate) Execute(now time.Time) error {
	if a.IsTerminal() {
		return ErrAlreadyTerminal
	}
	a.raise(MovementExecuted{
		OccurrenceID:        a.id,
		RecurringMovementID: a.recurringMovementID,
		ForDate:             a.forDate,
		ExecutedAt:          now,
	})
	return nil
}

// Skip closes the occurrence when the wallet refused.
func (a *Aggregate) Skip(reason string, now time.Time) error {
	if a.IsTerminal() {
		return ErrAlreadyTerminal
	}
	a.raise(MovementSkipped{
		OccurrenceID:        a.id,
		RecurringMovementID: a.recurringMovementID,
		ForDate:             a.forDate,
		Reason:              reason,
		SkippedAt:           now,
	})
	return nil
}

func (a *Aggregate) RecordAttempt(now time.Time) error {
	if a.IsTerminal() {
		return ErrAlreadyTerminal
	}
	a.raise(MovementRetryAttempted{
		OccurrenceID: a.id,
		Attempt:      a.attempts + 1,
		AttemptedAt:  now,
	})
	return nil
}

func (a *Aggregate) IsTerminal() bool {
	return a.status == StatusExecuted || a.status == StatusSkipped
}

// MoneyMoved reports whether this occurrence produced a movement. A skipped
// occurrence moved nothing, which is why giving up on one is always safe.
func (a *Aggregate) MoneyMoved() bool { return a.status == StatusExecuted }

func (a *Aggregate) ID() string                  { return a.id }
func (a *Aggregate) RecurringMovementID() string { return a.recurringMovementID }
func (a *Aggregate) WalletID() string            { return a.walletID }
func (a *Aggregate) UserID() string              { return a.userID }
func (a *Aggregate) Type() string                { return a.typ }
func (a *Aggregate) Amount() int                 { return a.amount }
func (a *Aggregate) Currency() string            { return a.currency }
func (a *Aggregate) Reason() string              { return a.reason }
func (a *Aggregate) ForDate() time.Time          { return a.forDate }
func (a *Aggregate) Status() string              { return a.status }
func (a *Aggregate) SkipReason() string          { return a.skipReason }
func (a *Aggregate) Attempts() int               { return a.attempts }
func (a *Aggregate) Version() int                { return a.version }
func (a *Aggregate) Changes() []DomainEvent      { return a.changes }

func (a *Aggregate) raise(event DomainEvent) {
	a.Apply(event)
	a.changes = append(a.changes, event)
}

func (a *Aggregate) Apply(event any) {
	switch e := event.(type) {
	case MovementDue:
		a.id = e.OccurrenceID
		a.recurringMovementID = e.RecurringMovementID
		a.walletID = e.WalletID
		a.userID = e.UserID
		a.typ = e.Type
		a.amount = e.Amount
		a.currency = e.Currency
		a.reason = e.Reason
		a.forDate = e.ForDate
		a.status = StatusDue
	case MovementExecuted:
		a.status = StatusExecuted
	case MovementSkipped:
		a.status = StatusSkipped
		a.skipReason = e.Reason
	case MovementRetryAttempted:
		a.attempts = e.Attempt
	}
	a.version++
}
