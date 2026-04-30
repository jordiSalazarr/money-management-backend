package recordrecurringmovement

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/recurringmovement"
	"github.com/jordisalazar/money-management/shared/eventstore"
)

type CommandHandler struct {
	store eventstore.Store
}

func NewCommandHandler(store eventstore.Store) *CommandHandler {
	return &CommandHandler{store: store}
}

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (string, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	agg, err := recurringmovement.Create(
		id, cmd.WalletID, cmd.UserID, cmd.Type,
		cmd.Amount, cmd.Currency, cmd.Frequency, cmd.Reason,
		cmd.StartDate, cmd.EndDate, now, today,
	)
	if err != nil {
		return "", err
	}

	envelopes := make([]eventstore.Event, 0, len(agg.Changes()))
	for _, change := range agg.Changes() {
		payload, err := json.Marshal(change)
		if err != nil {
			return "", err
		}
		envelopes = append(envelopes, eventstore.Event{
			EventID:       uuid.New().String(),
			AggregateID:   id,
			AggregateType: "recurring_movement",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    now,
		})
	}

	if err := h.store.Append(ctx, id, envelopes, 0); err != nil {
		return "", err
	}

	return id, nil
}
