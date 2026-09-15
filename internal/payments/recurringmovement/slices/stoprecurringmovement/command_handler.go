package stoprecurringmovement

import (
	"context"
	"time"

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

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (time.Time, error) {
	stream := streams.RecurringMovement(cmd.RecurringMovementID)

	agg := recurringmovement.NewAggregate()
	expected, err := eventstore.Rehydrate(ctx, h.store, h.codec, stream, agg.Apply)
	if err != nil {
		return time.Time{}, err
	}
	if expected == eventstore.NoStream {
		return time.Time{}, eventstore.ErrAggregateNotFound
	}

	now := time.Now().UTC()
	if err := agg.Stop(cmd.StoppedBy, cmd.Reason, now); err != nil {
		return time.Time{}, err
	}

	if err := eventstore.AppendChanges(ctx, h.store, h.codec, stream, expected,
		agg.Changes(), middleware.GetCorrelationID(ctx), now); err != nil {
		return time.Time{}, err
	}
	return now, nil
}
