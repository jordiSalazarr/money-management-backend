package executerecurringmovement

import (
	"context"
	"encoding/json"
	"fmt"
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

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) error {
	now := time.Now().UTC()

	events, err := h.store.Load(ctx, cmd.RecurringMovementID)
	if err != nil {
		return err
	}

	agg := recurringmovement.NewAggregate()
	for _, e := range events {
		raw, err := unmarshalEvent(e)
		if err != nil {
			return err
		}
		if raw != nil {
			agg.Apply(raw)
		}
	}

	if err := agg.Execute(cmd.ExecutedForDate, now); err != nil {
		return err
	}

	envelopes := make([]eventstore.Event, 0, len(agg.Changes()))
	for _, change := range agg.Changes() {
		payload, err := json.Marshal(change)
		if err != nil {
			return err
		}
		envelopes = append(envelopes, eventstore.Event{
			EventID:       uuid.New().String(),
			AggregateID:   cmd.RecurringMovementID,
			AggregateType: "recurring_movement",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    now,
		})
	}

	// Build outbox entry with all data the reactor needs
	outboxPayload, err := json.Marshal(RecurringMovementExecutedPayload{
		RecurringMovementID: agg.ID(),
		WalletID:            agg.WalletID(),
		UserID:              agg.UserID(),
		Type:                agg.Type(),
		Amount:              agg.Amount(),
		Currency:            agg.Currency(),
		Reason:              fmt.Sprintf("recurring:%s", agg.ID()),
	})
	if err != nil {
		return err
	}

	outboxEntries := []eventstore.OutboxEntry{
		{
			ID:        uuid.New().String(),
			EventType: recurringmovement.EventTypeRecurringMovementExecuted,
			Payload:   outboxPayload,
			CreatedAt: now,
		},
	}

	return h.store.AppendWithOutbox(ctx, cmd.RecurringMovementID, envelopes, len(events), outboxEntries)
}

func unmarshalEvent(e eventstore.Event) (any, error) {
	switch e.EventType {
	case recurringmovement.EventTypeRecurringMovementRegistered:
		var evt recurringmovement.RecurringMovementRegistered
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case recurringmovement.EventTypeRecurringMovementExecuted:
		var evt recurringmovement.RecurringMovementExecuted
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	default:
		return nil, nil
	}
}
