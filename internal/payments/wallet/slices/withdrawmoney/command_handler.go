package withdrawmoney

import (
	"context"
	"errors"
	"fmt"
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

// maxAttempts caps how many times Handle runs the withdrawal against fresh
// state when another write to the same wallet lands between load and append.
const maxAttempts = 3

// Handle withdraws money from a wallet. On a concurrency conflict it retries
// the whole command: a fresh aggregate, a fresh load, Withdraw evaluated again
// against the new state, and an append at the revision that load returned.
// Changes computed against one revision are never appended at another.
// Domain refusals are returned as they are; only a conflict is retried.
func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (int, error) {
	stream := streams.Wallet(cmd.WalletID)

	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var balance int
		balance, err = h.attempt(ctx, stream, cmd)
		if err == nil {
			return balance, nil
		}
		if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
			return 0, err
		}
	}
	return 0, fmt.Errorf("withdrawing after %d attempts: %w", maxAttempts, err)
}

// attempt runs one load-withdraw-append cycle and returns the balance of the
// aggregate whose changes were appended.
func (h *CommandHandler) attempt(ctx context.Context, stream eventstore.StreamID, cmd Command) (int, error) {
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
