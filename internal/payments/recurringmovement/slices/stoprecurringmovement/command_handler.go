package stoprecurringmovement

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type CommandHandler struct {
	store eventstore.Store
}

func NewCommandHandler(store eventstore.Store) *CommandHandler {
	return &CommandHandler{store: store}
}

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (time.Time, error) {
	now := time.Now().UTC()

	events, err := h.store.Load(ctx, cmd.RecurringMovementID)
	if err != nil {
		return time.Time{}, err
	}

	agg := recurringmovement.NewAggregate()
	for _, e := range events {
		raw, err := unmarshalEvent(e)
		if err != nil {
			return time.Time{}, err
		}
		if raw != nil {
			agg.Apply(raw)
		}
	}

	if err := agg.Stop(cmd.StoppedBy, cmd.Reason, now); err != nil {
		return time.Time{}, err
	}

	envelopes := make([]eventstore.Event, 0, len(agg.Changes()))
	for _, change := range agg.Changes() {
		payload, err := json.Marshal(change)
		if err != nil {
			return time.Time{}, err
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

	if err := h.store.Append(ctx, cmd.RecurringMovementID, envelopes, len(events)); err != nil {
		return time.Time{}, err
	}

	return now, nil
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
	case recurringmovement.EventTypeRecurringMovementStopped:
		var evt recurringmovement.RecurringMovementStopped
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	default:
		return nil, nil
	}
}
