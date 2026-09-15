package settledebt

import (
	"context"
	"time"

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

func (h *CommandHandler) Handle(ctx context.Context, cmd Command) error {
	stream := streams.Debt(cmd.DebtID)

	agg := debt.NewAggregate()
	expected, err := eventstore.Rehydrate(ctx, h.store, h.codec, stream, agg.Apply)
	if err != nil {
		return err
	}
	if expected == eventstore.NoStream {
		return eventstore.ErrAggregateNotFound
	}

	now := time.Now().UTC()
	// Raises DebtSettlementRequested. The debt is not settled until the
	// reactor has actually moved the money — see ADR 0002.
	if err := agg.RequestSettlement(cmd.SettledBy, now); err != nil {
		return err
	}

	return eventstore.AppendChanges(ctx, h.store, h.codec, stream, expected,
		agg.Changes(), middleware.GetCorrelationID(ctx), now)
}
