package recurringmovement

import "time"

var validCurrencies = map[string]bool{"EUR": true, "USD": true}
var validFrequencies = map[string]bool{"DAILY": true, "WEEKLY": true, "MONTHLY": true}
var validTypes = map[string]bool{"deposit": true, "withdrawal": true}

type DomainEvent interface {
	EventType() string
}

type Aggregate struct {
	id               string
	walletID         string
	userID           string
	typ              string
	amount           int
	currency         string
	frequency        string
	reason           string
	startDate        time.Time
	endDate          time.Time
	active           bool
	lastExecutedDate time.Time
	version          int
	changes          []DomainEvent
}

func NewAggregate() *Aggregate {
	return &Aggregate{}
}

func Create(
	id, walletID, userID, typ string,
	amount int,
	currency, frequency, reason string,
	startDate, endDate time.Time,
	now time.Time,
	today time.Time,
) (*Aggregate, error) {
	if walletID == "" {
		return nil, ErrMissingWalletID
	}
	if userID == "" {
		return nil, ErrMissingUserID
	}
	if !validTypes[typ] {
		return nil, ErrInvalidType
	}
	if amount <= 0 || amount > 10_000_000 {
		return nil, ErrInvalidAmount
	}
	if !validCurrencies[currency] {
		return nil, ErrInvalidCurrency
	}
	if !validFrequencies[frequency] {
		return nil, ErrInvalidFrequency
	}
	if reason == "" {
		return nil, ErrMissingReason
	}
	if startDate.Before(today) {
		return nil, ErrInvalidStartDate
	}
	if !endDate.IsZero() && !endDate.After(startDate) {
		return nil, ErrInvalidEndDate
	}

	agg := &Aggregate{}
	agg.raise(RecurringMovementRegistered{
		RecurringMovementID: id,
		WalletID:            walletID,
		UserID:              userID,
		Type:                typ,
		Amount:              amount,
		Currency:            currency,
		Frequency:           frequency,
		Reason:              reason,
		StartDate:           startDate,
		EndDate:             endDate,
		RegisteredAt:        now,
	})
	return agg, nil
}

func (a *Aggregate) ID() string             { return a.id }
func (a *Aggregate) WalletID() string       { return a.walletID }
func (a *Aggregate) UserID() string         { return a.userID }
func (a *Aggregate) Type() string           { return a.typ }
func (a *Aggregate) Amount() int            { return a.amount }
func (a *Aggregate) Currency() string       { return a.currency }
func (a *Aggregate) Frequency() string      { return a.frequency }
func (a *Aggregate) Reason() string         { return a.reason }
func (a *Aggregate) StartDate() time.Time   { return a.startDate }
func (a *Aggregate) EndDate() time.Time     { return a.endDate }
func (a *Aggregate) Active() bool                { return a.active }
func (a *Aggregate) LastExecutedDate() time.Time  { return a.lastExecutedDate }
func (a *Aggregate) Version() int                 { return a.version }
func (a *Aggregate) Changes() []DomainEvent       { return a.changes }

func (a *Aggregate) Stop(stoppedBy string, reason string, now time.Time) error {
	if !a.active {
		return ErrNotActive
	}
	if stoppedBy != a.userID {
		return ErrNotOwner
	}
	a.raise(RecurringMovementStopped{
		RecurringMovementID: a.id,
		StoppedBy:           stoppedBy,
		Reason:              reason,
		StoppedAt:           now,
	})
	return nil
}

func (a *Aggregate) Execute(executedForDate time.Time, now time.Time) error {
	if !a.active {
		return ErrNotActive
	}
	if !a.lastExecutedDate.IsZero() && !a.lastExecutedDate.Before(executedForDate) {
		return ErrAlreadyExecutedToday
	}
	a.raise(RecurringMovementExecuted{
		RecurringMovementID: a.id,
		ExecutedForDate:     executedForDate,
		ExecutedAt:          now,
	})
	return nil
}

func (a *Aggregate) raise(event DomainEvent) {
	a.Apply(event)
	a.changes = append(a.changes, event)
}

func (a *Aggregate) Apply(event any) {
	switch e := event.(type) {
	case RecurringMovementRegistered:
		a.id = e.RecurringMovementID
		a.walletID = e.WalletID
		a.userID = e.UserID
		a.typ = e.Type
		a.amount = e.Amount
		a.currency = e.Currency
		a.frequency = e.Frequency
		a.reason = e.Reason
		a.startDate = e.StartDate
		a.endDate = e.EndDate
		a.active = true
	case RecurringMovementExecuted:
		a.lastExecutedDate = e.ExecutedForDate
	case RecurringMovementStopped:
		a.active = false
	}
	a.version++
}
