package transaction

import "time"

const (
	StatusCreated   = "CREATED"
	StatusCompleted = "COMPLETED"
)

type DomainEvent interface {
	EventType() string
}

type Aggregate struct {
	id           string
	status       string
	userID       string
	fromWalletID string
	toWalletID   string
	amount       int
	currency     string
	reason       string
	version      int
	changes      []DomainEvent
}

func NewAggregate() *Aggregate {
	return &Aggregate{}
}

func Create(transactionID, userID, fromWalletID, toWalletID string, amount int, currency, reason string, now time.Time) (*Aggregate, error) {
	agg := &Aggregate{}
	agg.raise(TransactionCreated{
		TransactionID: transactionID,
		UserID:        userID,
		FromWalletID:  fromWalletID,
		ToWalletID:    toWalletID,
		Amount:        amount,
		Currency:      currency,
		Reason:        reason,
		CreatedAt:     now,
	})
	return agg, nil
}

func (a *Aggregate) Complete(now time.Time) error {
	if a.status == StatusCompleted {
		return ErrAlreadyCompleted
	}

	a.raise(TransactionCompleted{
		TransactionID: a.id,
		UserID:        a.userID,
		FromWalletID:  a.fromWalletID,
		ToWalletID:    a.toWalletID,
		Amount:        a.amount,
		Currency:      a.currency,
		Reason:        a.reason,
		CompletedAt:   now,
	})
	return nil
}

func (a *Aggregate) ID() string             { return a.id }
func (a *Aggregate) Status() string         { return a.status }
func (a *Aggregate) Version() int           { return a.version }
func (a *Aggregate) Changes() []DomainEvent { return a.changes }

func (a *Aggregate) raise(event DomainEvent) {
	a.Apply(event)
	a.changes = append(a.changes, event)
}

func (a *Aggregate) Apply(event any) {
	switch e := event.(type) {
	case TransactionCreated:
		a.id = e.TransactionID
		a.status = StatusCreated
		a.userID = e.UserID
		a.fromWalletID = e.FromWalletID
		a.toWalletID = e.ToWalletID
		a.amount = e.Amount
		a.currency = e.Currency
		a.reason = e.Reason
	case TransactionCompleted:
		a.status = StatusCompleted
	}
	a.version++
}
