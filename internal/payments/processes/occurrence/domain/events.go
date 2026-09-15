package occurrence

import "time"

const (
	EventTypeMovementDue            = "MovementDue"
	EventTypeMovementExecuted       = "MovementExecuted"
	EventTypeMovementSkipped        = "MovementSkipped"
	EventTypeMovementRetryAttempted = "MovementRetryAttempted"
)

// MovementDue is the cron saying a recurring movement should fire today. It is
// an instruction, not a fact: no money has moved.
//
// It lives on its own stream, occurrence-<rmID>-<date>, and is appended at
// ExpectedRevision.NoStream. A second cron run for the same date therefore
// fails with WrongExpectedVersion — the stream name IS the idempotency key.
type MovementDue struct {
	OccurrenceID        string    `json:"occurrence_id"`
	RecurringMovementID string    `json:"recurring_movement_id"`
	WalletID            string    `json:"wallet_id"`
	UserID              string    `json:"user_id"`
	Type                string    `json:"type"`
	Amount              int       `json:"amount"`
	Currency            string    `json:"currency"`
	Reason              string    `json:"reason"`
	ForDate             time.Time `json:"for_date"`
	DueAt               time.Time `json:"due_at"`
}

func (e MovementDue) EventType() string { return EventTypeMovementDue }

// MovementExecuted means the money actually moved.
//
// The event this replaces, RecurringMovementExecuted, was written *before* the
// wallet command ran. When that command was refused — an ordinary month with a
// short balance was enough — the record was dropped and the date was marked
// executed anyway. The standing order silently skipped a month and the read
// model said it had run.
type MovementExecuted struct {
	OccurrenceID        string    `json:"occurrence_id"`
	RecurringMovementID string    `json:"recurring_movement_id"`
	ForDate             time.Time `json:"for_date"`
	ExecutedAt          time.Time `json:"executed_at"`
}

func (e MovementExecuted) EventType() string { return EventTypeMovementExecuted }

// MovementSkipped means the wallet refused, so no money moved for this date.
//
// It is a first-class, user-facing outcome, not an error: "your standing order
// was skipped this month — insufficient funds" is what a bank tells you.
type MovementSkipped struct {
	OccurrenceID        string    `json:"occurrence_id"`
	RecurringMovementID string    `json:"recurring_movement_id"`
	ForDate             time.Time `json:"for_date"`
	Reason              string    `json:"reason"`
	SkippedAt           time.Time `json:"skipped_at"`
}

func (e MovementSkipped) EventType() string { return EventTypeMovementSkipped }

// MovementRetryAttempted records that the sweeper re-drove this occurrence.
type MovementRetryAttempted struct {
	OccurrenceID string    `json:"occurrence_id"`
	Attempt      int       `json:"attempt"`
	AttemptedAt  time.Time `json:"attempted_at"`
}

func (e MovementRetryAttempted) EventType() string { return EventTypeMovementRetryAttempted }
