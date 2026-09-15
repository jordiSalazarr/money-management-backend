package requesttransfer

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
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

// Handle validates what can be validated cheaply, writes TransferRequested to
// a single stream, and returns. It does not move any money — the reactor does
// that, asynchronously, which is why the endpoint answers 202 and not 201.
func (h *CommandHandler) Handle(ctx context.Context, cmd Command) (string, error) {
	transferID := uuid.New().String()
	now := time.Now().UTC()

	agg, err := transfer.Request(transferID, cmd.UserID, cmd.FromWalletID, cmd.ToWalletID,
		cmd.Amount, cmd.Currency, cmd.Reason, now)
	if err != nil {
		return "", err
	}

	// An advisory precheck, so the obvious mistakes fail fast with a 4xx
	// instead of being accepted and then failing asynchronously. It is NOT a
	// guarantee: the authoritative check happens inside wallet.Withdraw when
	// the reactor runs it, and by then the balance may have changed.
	if err := h.precheck(ctx, cmd); err != nil {
		return "", err
	}

	if err := eventstore.AppendChanges(ctx, h.store, h.codec, streams.Transfer(transferID),
		eventstore.NoStream, agg.Changes(), middleware.GetCorrelationID(ctx), now); err != nil {
		return "", err
	}
	return transferID, nil
}

// precheck loads both wallets read-only. It deliberately checks currency as
// well as balance — the old implementation checked balance only, which is how
// a EUR-to-USD transfer could be accepted, debited, and then fail on the
// credit leg with the money already out of the source wallet.
func (h *CommandHandler) precheck(ctx context.Context, cmd Command) error {
	from, err := h.loadWallet(ctx, cmd.FromWalletID)
	if err != nil {
		return err
	}
	to, err := h.loadWallet(ctx, cmd.ToWalletID)
	if err != nil {
		return err
	}
	if from.Currency() != cmd.Currency || to.Currency() != cmd.Currency {
		return wallet.ErrCurrencyMismatch
	}
	if from.Disabled() || to.Disabled() {
		return wallet.ErrWalletDisabled
	}
	if from.Balance() < cmd.Amount {
		return wallet.ErrInsufficientFunds
	}
	return nil
}

func (h *CommandHandler) loadWallet(ctx context.Context, walletID string) (*wallet.Aggregate, error) {
	agg := wallet.NewAggregate()
	expected, err := eventstore.Rehydrate(ctx, h.store, h.codec, streams.Wallet(walletID), agg.Apply)
	if err != nil {
		return nil, err
	}
	if expected == eventstore.NoStream {
		return nil, fmt.Errorf("wallet %s: %w", walletID, eventstore.ErrAggregateNotFound)
	}
	return agg, nil
}
