package settledebt

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/payments/debt"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type CommandHandler struct {
	store eventstore.Store
}

func NewCommandHandler(store eventstore.Store) *CommandHandler {
	return &CommandHandler{store: store}
}

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) error {
	now := time.Now().UTC()

	events, err := h.store.Load(ctx, cmd.DebtID)
	if err != nil {
		return err
	}

	agg := debt.NewAggregate()
	for _, e := range events {
		raw, err := unmarshalDebtEvent(e)
		if err != nil {
			return err
		}
		if raw != nil {
			agg.Apply(raw)
		}
	}

	if err := agg.Settle(cmd.SettledBy, now); err != nil {
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
			AggregateID:   cmd.DebtID,
			AggregateType: "debt",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    now,
		})
	}

	// The reactor that turns a settled debt into a wallet deposit/withdrawal
	// is wired up in phase 3 (Kinesis consumer Lambda). Phase 2 just persists
	// the DebtSettled event; settlement won't move money until that lands.
	return h.store.Append(ctx, cmd.DebtID, envelopes, len(events))
}

func unmarshalDebtEvent(e eventstore.Event) (any, error) {
	switch e.EventType {
	case debt.EventTypeDebtRecorded:
		var evt debt.DebtRecorded
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case debt.EventTypeDebtSettled:
		var evt debt.DebtSettled
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	default:
		return nil, nil
	}
}
