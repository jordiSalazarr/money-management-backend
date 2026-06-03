package disablewallet

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/payments/wallet"
)

type CommandHandler struct {
	store eventstore.Store
}

func NewCommandHandler(store eventstore.Store) *CommandHandler {
	return &CommandHandler{store: store}
}

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) error {
	events, err := h.store.Load(ctx, cmd.WalletID)
	if err != nil {
		return err
	}

	agg := wallet.NewAggregate()
	for _, e := range events {
		raw, err := unmarshalEvent(e)
		if err != nil {
			return err
		}
		agg.Apply(raw)
	}

	if err := agg.Disable(cmd.UserID, cmd.Reason, cmd.DisabledAt); err != nil {
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
			AggregateID:   cmd.WalletID,
			AggregateType: "wallet",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    cmd.DisabledAt,
		})
	}

	if err := h.store.Append(ctx, cmd.WalletID, envelopes, len(events)); err != nil {
		return err
	}

	return nil
}

func unmarshalEvent(e eventstore.Event) (any, error) {
	switch e.EventType {
	case wallet.EventTypeWalletAdded:
		var evt wallet.WalletAdded
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case wallet.EventTypeMoneyDeposited:
		var evt wallet.MoneyDeposited
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case wallet.EventTypeMoneyWithdrawn:
		var evt wallet.MoneyWithdrawn
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case wallet.EventTypeWalletDisabled:
		var evt wallet.WalletDisabled
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	default:
		return nil, nil
	}
}
