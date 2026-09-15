package recorddebt

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	debt "github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type CommandHandler struct {
	store eventstore.Store
}

func NewCommandHandler(store eventstore.Store) *CommandHandler {
	return &CommandHandler{store: store}
}

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (string, error) {
	debtID := uuid.New().String()
	now := time.Now().UTC()

	agg, err := debt.Create(debtID, cmd.OwnerID, cmd.WalletID, cmd.Amount, cmd.Currency, cmd.Direction, cmd.Reason, now)
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
			AggregateID:   debtID,
			AggregateType: "debt",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    now,
		})
	}

	if err := h.store.Append(ctx, debtID, envelopes, 0); err != nil {
		return "", err
	}

	return debtID, nil
}
