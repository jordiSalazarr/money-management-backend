package recorddebt

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
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
	debtID := uuid.New().String()
	now := time.Now().UTC()

	agg, err := debt.Create(debtID, cmd.OwnerID, cmd.WalletID, cmd.Amount, cmd.Currency, cmd.Direction, cmd.Reason, now)
	if err != nil {
		return "", err
	}

	if err := eventstore.AppendChanges(ctx, h.store, h.codec, streams.Debt(debtID),
		eventstore.NoStream, agg.Changes(), middleware.GetCorrelationID(ctx), now); err != nil {
		return "", err
	}
	return debtID, nil
}
