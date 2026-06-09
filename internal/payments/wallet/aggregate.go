package wallet

import "time"

var validCurrencies = map[string]bool{"EUR": true, "USD": true}
var validCategories = map[string]bool{
	"Cash": true, "Checking": true, "Savings": true, "Credit Card": true,
	"Investment": true, "Digital Wallet": true, "Loan": true, "Other": true,
}
var validWithdrawCategories = map[string]bool{
	"Housing": true, "Food & Groceries": true, "Dining Out": true, "Transport": true,
	"Health & Medical": true, "Entertainment": true, "Education": true, "Clothing": true,
	"Subscriptions": true, "Travel": true, "Gifts & Donations": true, "Insurance": true,
	"Utilities": true, "Personal Care": true, "Pets": true, "Other": true,
}
var validDepositCategories = map[string]bool{
	"Salary": true, "Freelance": true, "Investment Return": true, "Refund": true,
	"Gift": true, "Transfer In": true, "Rental Income": true, "Side Hustle": true,
	"Other": true,
}

const (
	MaxInitialAmount = 10_000_000
	MinInitialAmount = 0
)

// TODO: this should be shared
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
	disabled  bool
	version   int
	createdAt time.Time
	changes   []DomainEvent
}

func (a *Aggregate) Type() string {
	return "wallet"
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
	if initialAmount < MinInitialAmount || initialAmount > MaxInitialAmount {
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

func (a *Aggregate) ID() string             { return a.id }
func (a *Aggregate) Version() int           { return a.version }
func (a *Aggregate) Balance() int           { return a.balance }
func (a *Aggregate) Currency() string       { return a.currency }
func (a *Aggregate) Category() string       { return a.category }
func (a *Aggregate) Changes() []DomainEvent { return a.changes }

func (a *Aggregate) Disabled() bool { return a.disabled }

func (a *Aggregate) Disable(userID, reason string, now time.Time) error {
	if a.disabled {
		return ErrWalletAlreadyDisabled
	}

	a.raise(WalletDisabled{
		WalletID:   a.id,
		UserID:     userID,
		DisabledAt: now,
		Reason:     reason,
	})
	return nil
}

func (a *Aggregate) Deposit(userID string, amount int, currency, reason, category string, now time.Time) error {
	if a.disabled {
		return ErrWalletDisabled
	}
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if currency != a.currency {
		return ErrCurrencyMismatch
	}
	if !validDepositCategories[category] {
		return ErrInvalidDepositCategory
	}

	a.raise(MoneyDeposited{
		WalletID:    a.id,
		UserID:      userID,
		Amount:      amount,
		Currency:    currency,
		Reason:      reason,
		Category:    category,
		DepositedAt: now,
	})
	return nil
}

func (a *Aggregate) Withdraw(userID string, amount int, currency, reason, category string, now time.Time) error {
	if a.disabled {
		return ErrWalletDisabled
	}
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
	case WalletDisabled:
		a.disabled = true
	}
	a.version++
}
