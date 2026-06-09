package withdrawmoney

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/payments/wallet"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type CommandHandler struct {
	store eventstore.Store
}

func NewCommandHandler(store eventstore.Store) *CommandHandler {
	return &CommandHandler{store: store}
}

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (int, error) {
	events, err := h.store.Load(ctx, cmd.WalletID)
	if err != nil {
		return 0, err
	}

	agg := wallet.NewAggregate()
	for _, e := range events {
		raw, err := unmarshalEvent(e)
		if err != nil {
			return 0, err
		}
		agg.Apply(raw)
	}

	now := time.Now().UTC()
	if err := agg.Withdraw(cmd.UserID, cmd.Amount, cmd.Currency, cmd.Reason, cmd.Category, cmd.IdempotencyKey, now); err != nil {
		return 0, err
	}

	envelopes := make([]eventstore.Event, 0, len(agg.Changes()))
	for _, change := range agg.Changes() {
		payload, err := json.Marshal(change)
		if err != nil {
			return 0, err
		}
		envelopes = append(envelopes, eventstore.Event{
			EventID:       uuid.New().String(),
			AggregateID:   cmd.WalletID,
			AggregateType: "wallet",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    now,
		})
	}

	if err := h.store.Append(ctx, cmd.WalletID, envelopes, len(events)); err != nil {
		return 0, err
	}

	return agg.Balance(), nil
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
