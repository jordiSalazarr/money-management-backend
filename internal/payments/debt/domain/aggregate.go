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
	DebtStatusCreated DebtStatus = "created"
	DebtStatusSettled DebtStatus = "settled"
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
func (a *Aggregate) Changes() []DomainEvent { return a.changes }

func (a *Aggregate) Settle(settledBy string, now time.Time) error {
	if settledBy == "" {
		return ErrMissingSettledBy
	}
	if a.status == DebtStatusSettled {
		return ErrDebtAlreadySettled
	}
	a.raise(DebtSettled{
		DebtID:    a.id,
		SettledBy: settledBy,
		SettledAt: now,
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
	case DebtSettled:
		a.status = DebtStatusSettled
	}
	a.version++
}
