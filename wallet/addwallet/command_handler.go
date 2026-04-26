package addwallet

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/wallet"
)

type CommandHandler struct {
	store eventstore.Store
}

func NewCommandHandler(store eventstore.Store) *CommandHandler {
	return &CommandHandler{store: store}
}

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (string, error) {
	walletID := uuid.New().String()
	now := time.Now().UTC()

	agg, err := wallet.Create(walletID, cmd.UserID, cmd.Name, cmd.Description, cmd.InitialAmount, cmd.Currency, cmd.Category, now)
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
			AggregateID:   walletID,
			AggregateType: "wallet",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    now,
		})
	}

	if err := h.store.Append(ctx, walletID, envelopes, 0); err != nil {
		return "", err
	}

	return walletID, nil
}
