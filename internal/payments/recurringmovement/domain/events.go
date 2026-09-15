package recurringmovement

import "time"

const (
	EventTypeRecurringMovementRegistered = "RecurringMovementRegistered"
	EventTypeRecurringMovementStopped    = "RecurringMovementStopped"
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

type RecurringMovementStopped struct {
	RecurringMovementID string    `json:"recurring_movement_id"`
	StoppedBy           string    `json:"stopped_by"`
	Reason              string    `json:"reason"`
	StoppedAt           time.Time `json:"stopped_at"`
}

func (e RecurringMovementStopped) EventType() string {
	return EventTypeRecurringMovementStopped
}
