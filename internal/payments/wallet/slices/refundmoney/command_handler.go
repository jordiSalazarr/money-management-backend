package refundmoney

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

func (h *CommandHandler) Handle(ctx context.Context, cmd Command, now time.Time) error {
	stream := streams.Wallet(cmd.WalletID)

	agg := wallet.NewAggregate()
	expected, err := eventstore.Rehydrate(ctx, h.store, h.codec, stream, agg.Apply)
	if err != nil {
		return err
	}
	if expected == eventstore.NoStream {
		return eventstore.ErrAggregateNotFound
	}

	if err := agg.Refund(cmd.UserID, cmd.Amount, cmd.Currency, cmd.Reason, cmd.IdempotencyKey, now); err != nil {
		return err
	}

	return eventstore.AppendChanges(ctx, h.store, h.codec, stream, expected,
		agg.Changes(), middleware.GetCorrelationID(ctx), now)
}
