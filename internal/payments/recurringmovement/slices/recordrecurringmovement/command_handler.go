package recordrecurringmovement

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

type CommandHandler struct {
	store eventstore.Store
	codec *eventstore.Codec
}

func NewCommandHandler(store eventstore.Store, codec *eventstore.Codec) *CommandHandler {
	return &CommandHandler{store: store, codec: codec}
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

	if err := eventstore.AppendChanges(ctx, h.store, h.codec, streams.RecurringMovement(id),
		eventstore.NoStream, agg.Changes(), middleware.GetCorrelationID(ctx), now); err != nil {
		return "", err
	}
	return id, nil
}
