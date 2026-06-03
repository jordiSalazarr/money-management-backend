package executerecurringmovement

import "time"

type Command struct {
	RecurringMovementID string
	ExecutedForDate     time.Time
}
