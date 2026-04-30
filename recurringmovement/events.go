package recurringmovement

import "time"

const (
	EventTypeRecurringMovementRegistered = "RecurringMovementRegistered"
	EventTypeRecurringMovementExecuted   = "RecurringMovementExecuted"
)

type RecurringMovementRegistered struct {
	RecurringMovementID string    `json:"recurring_movement_id"`
	WalletID            string    `json:"wallet_id"`
	UserID              string    `json:"user_id"`
	Type                string    `json:"type"`
	Amount              int       `json:"amount"`
	Currency            string    `json:"currency"`
	Frequency           string    `json:"frequency"`
	Reason              string    `json:"reason"`
	StartDate           time.Time `json:"start_date"`
	EndDate             time.Time `json:"end_date"`
	RegisteredAt        time.Time `json:"registered_at"`
}

func (e RecurringMovementRegistered) EventType() string {
	return EventTypeRecurringMovementRegistered
}

type RecurringMovementExecuted struct {
	RecurringMovementID string    `json:"recurring_movement_id"`
	ExecutedForDate     time.Time `json:"executed_for_date"`
	ExecutedAt          time.Time `json:"executed_at"`
}

func (e RecurringMovementExecuted) EventType() string {
	return EventTypeRecurringMovementExecuted
}
