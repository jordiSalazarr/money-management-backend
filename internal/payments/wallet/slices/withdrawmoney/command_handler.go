package withdrawmoney

import (
	"context"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/streams"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
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

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (int, error) {
	stream := streams.Wallet(cmd.WalletID)

	agg := wallet.NewAggregate()
	expected, err := eventstore.Rehydrate(ctx, h.store, h.codec, stream, agg.Apply)
	if err != nil {
		return 0, err
	}
	if expected == eventstore.NoStream {
		return 0, eventstore.ErrAggregateNotFound
	}

	now := time.Now().UTC()
	if err := agg.Withdraw(cmd.UserID, cmd.Amount, cmd.Currency, cmd.Reason, cmd.Category, cmd.IdempotencyKey, now); err != nil {
		return 0, err
	}

	if err := eventstore.AppendChanges(ctx, h.store, h.codec, stream, expected,
		agg.Changes(), middleware.GetCorrelationID(ctx), now); err != nil {
		return 0, err
	}
	return agg.Balance(), nil
}
