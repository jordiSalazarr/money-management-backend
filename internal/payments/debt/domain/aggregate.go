package debt

import "time"

var validCurrencies = map[string]bool{"EUR": true, "USD": true}
var validDirections = map[string]bool{"IN": true, "OUT": true}
var maxRecordAmount = 10000000

type DomainEvent interface {
	EventType() string
}

type DebtStatus string

const (
	DebtStatusCreated  DebtStatus = "created"
	DebtStatusSettling DebtStatus = "settling"
	DebtStatusSettled  DebtStatus = "settled"
)

type Aggregate struct {
	id        string
	ownerID   string
	walletID  string
	amount    int
	currency  string
	direction string
	reason    string
	status    DebtStatus
	attempts  int
	createdAt time.Time
	version   int
	changes   []DomainEvent
}

func NewAggregate() *Aggregate {
	return &Aggregate{}
}

func Create(debtID, ownerID, walletID string, amount int, currency, direction, reason string, now time.Time) (*Aggregate, error) {
	if ownerID == "" {
		return nil, ErrMissingOwnerID
	}
	if walletID == "" {
		return nil, ErrMissingWalletID
	}
	if amount <= 0 || amount > maxRecordAmount {
		return nil, ErrInvalidAmount
	}
	if !validCurrencies[currency] {
		return nil, ErrInvalidCurrency
	}
	if !validDirections[direction] {
		return nil, ErrInvalidDirection
	}
	if reason == "" {
		return nil, ErrMissingReason
	}

	agg := &Aggregate{}
	agg.raise(DebtRecorded{
		DebtID:    debtID,
		WalletID:  walletID,
		OwnerID:   ownerID,
		Amount:    amount,
		Currency:  currency,
		Direction: direction,
		Reason:    reason,
		CreatedAt: now,
	})
	return agg, nil
}

func (a *Aggregate) ID() string             { return a.id }
func (a *Aggregate) Version() int           { return a.version }
func (a *Aggregate) OwnerID() string        { return a.ownerID }
func (a *Aggregate) WalletID() string       { return a.walletID }
func (a *Aggregate) Amount() int            { return a.amount }
func (a *Aggregate) Currency() string       { return a.currency }
func (a *Aggregate) Direction() string      { return a.direction }
func (a *Aggregate) Reason() string         { return a.reason }
func (a *Aggregate) Status() DebtStatus     { return a.status }
func (a *Aggregate) Attempts() int          { return a.attempts }
func (a *Aggregate) Changes() []DomainEvent { return a.changes }

// RequestSettlement records that the owner wants to settle. It does not mark
// the debt settled — the money has not moved yet. See ADR 0002.
func (a *Aggregate) RequestSettlement(settledBy string, now time.Time) error {
	if settledBy == "" {
		return ErrMissingSettledBy
	}
	if a.status == DebtStatusSettled {
		return ErrDebtAlreadySettled
	}
	if a.status == DebtStatusSettling {
		return ErrSettlementInProgress
	}
	a.raise(DebtSettlementRequested{
		DebtID:      a.id,
		RequestedBy: settledBy,
		RequestedAt: now,
	})
	return nil
}

// MarkSettled closes the settlement once the wallet movement has gone through.
// This is the only place the debt becomes settled, and by the time it runs the
// money has already moved.
func (a *Aggregate) MarkSettled(settledBy string, now time.Time) error {
	if a.status == DebtStatusSettled {
		return ErrDebtAlreadySettled
	}
	if a.status != DebtStatusSettling {
		return ErrSettlementNotRequested
	}
	a.raise(DebtSettled{
		DebtID:    a.id,
		SettledBy: settledBy,
		SettledAt: now,
	})
	return nil
}

// FailSettlement records that the wallet refused the movement, so the debt
// stands and can be settled again later.
func (a *Aggregate) FailSettlement(reason string, now time.Time) error {
	if a.status != DebtStatusSettling {
		return ErrSettlementNotRequested
	}
	a.raise(DebtSettlementFailed{
		DebtID:   a.id,
		Reason:   reason,
		FailedAt: now,
	})
	return nil
}

// RecordSettlementAttempt notes a sweeper re-drive of the settlement in flight.
func (a *Aggregate) RecordSettlementAttempt(now time.Time) error {
	if a.status != DebtStatusSettling {
		return ErrSettlementNotRequested
	}
	a.raise(DebtSettlementRetryAttempted{
		DebtID:      a.id,
		Attempt:     a.attempts + 1,
		AttemptedAt: now,
	})
	return nil
}

func (a *Aggregate) raise(event DomainEvent) {
	a.Apply(event)
	a.changes = append(a.changes, event)
}

func (a *Aggregate) Apply(event any) {
	switch e := event.(type) {
	case DebtRecorded:
		a.id = e.DebtID
		a.ownerID = e.OwnerID
		a.walletID = e.WalletID
		a.amount = e.Amount
		a.currency = e.Currency
		a.direction = e.Direction
		a.reason = e.Reason
		a.status = DebtStatusCreated
		a.createdAt = e.CreatedAt
	case DebtSettlementRequested:
		a.status = DebtStatusSettling
		// A fresh attempt starts its budget over.
		a.attempts = 0
	case DebtSettlementRetryAttempted:
		a.attempts = e.Attempt
	case DebtSettled:
		a.status = DebtStatusSettled
	case DebtSettlementFailed:
		// The debt stands. It can be settled again.
		a.status = DebtStatusCreated
	}
	a.version++
}
