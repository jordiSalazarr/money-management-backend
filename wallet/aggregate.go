package wallet

import "time"

var validCurrencies = map[string]bool{"EUR": true, "USD": true}
var validCategories = map[string]bool{"Saving": true, "Investment": true, "Cash": true}
var validWithdrawCategories = map[string]bool{
	"Food": true, "Investment": true, "Clothing": true, "Travel": true,
	"Entertainment": true, "Health": true, "Housing": true, "Transport": true,
	"Education": true, "Subscriptions": true, "Gifts": true, "Other": true,
}

type DomainEvent interface {
	EventType() string
}

type Aggregate struct {
	id        string
	userID    string
	name      string
	currency  string
	category  string
	balance   int
	version   int
	createdAt time.Time
	changes   []DomainEvent
}

func NewAggregate() *Aggregate {
	return &Aggregate{}
}

func Create(walletID, userID, name, description string, initialAmount int, currency, category string, now time.Time) (*Aggregate, error) {
	if userID == "" {
		return nil, ErrMissingUserID
	}
	if name == "" {
		return nil, ErrMissingName
	}
	if initialAmount < 0 || initialAmount > 10_000_000 {
		return nil, ErrInvalidInitialAmount
	}
	if !validCurrencies[currency] {
		return nil, ErrInvalidCurrency
	}
	if !validCategories[category] {
		return nil, ErrInvalidCategory
	}

	agg := &Aggregate{}
	agg.raise(WalletAdded{
		WalletID:      walletID,
		UserID:        userID,
		Name:          name,
		Description:   description,
		InitialAmount: initialAmount,
		Currency:      currency,
		Category:      category,
		CreatedAt:     now,
	})
	return agg, nil
}

func (a *Aggregate) ID() string              { return a.id }
func (a *Aggregate) Version() int            { return a.version }
func (a *Aggregate) Balance() int            { return a.balance }
func (a *Aggregate) Currency() string        { return a.currency }
func (a *Aggregate) Category() string        { return a.category }
func (a *Aggregate) Changes() []DomainEvent  { return a.changes }

func (a *Aggregate) Deposit(userID string, amount int, currency, reason string, now time.Time) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if currency != a.currency {
		return ErrCurrencyMismatch
	}

	a.raise(MoneyDeposited{
		WalletID:    a.id,
		UserID:      userID,
		Amount:      amount,
		Currency:    currency,
		Reason:      reason,
		DepositedAt: now,
	})
	return nil
}

func (a *Aggregate) Withdraw(userID string, amount int, currency, reason, category string, now time.Time) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if currency != a.currency {
		return ErrCurrencyMismatch
	}
	if !validWithdrawCategories[category] {
		return ErrInvalidWithdrawCategory
	}
	if a.balance < amount {
		return ErrInsufficientFunds
	}

	a.raise(MoneyWithdrawn{
		WalletID:    a.id,
		UserID:      userID,
		Amount:      amount,
		Currency:    currency,
		Reason:      reason,
		Category:    category,
		WithdrawnAt: now,
	})
	return nil
}

func (a *Aggregate) raise(event DomainEvent) {
	a.Apply(event)
	a.changes = append(a.changes, event)
}

func (a *Aggregate) Apply(event any) {
	switch e := event.(type) {
	case WalletAdded:
		a.id = e.WalletID
		a.userID = e.UserID
		a.name = e.Name
		a.currency = e.Currency
		a.category = e.Category
		a.balance = e.InitialAmount
		a.createdAt = e.CreatedAt
	case MoneyDeposited:
		a.balance += e.Amount
	case MoneyWithdrawn:
		a.balance -= e.Amount
	}
	a.version++
}
