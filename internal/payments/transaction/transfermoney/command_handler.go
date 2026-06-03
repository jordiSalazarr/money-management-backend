package transfermoney

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/payments/transaction"
	"github.com/jordisalazar/money-management/internal/payments/wallet"
	walletservices "github.com/jordisalazar/money-management/internal/payments/wallet/services"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type CommandHandler struct {
	store eventstore.Store
}

func NewCommandHandler(store eventstore.Store) *CommandHandler {
	return &CommandHandler{store: store}
}

func (h *CommandHandler) Handle(ctx context.Context, cmd walletservices.TransferMoneyCommand) (string, error) {
	// Step 1: Validate command-level invariants
	if cmd.Amount <= 0 {
		return "", wallet.ErrInvalidAmount
	}
	if cmd.UserID == "" {
		return "", wallet.ErrMissingUserID
	}
	if cmd.FromWalletID == cmd.ToWalletID {
		return "", ErrSameWallet
	}

	now := time.Now().UTC()

	// Step 2: Load source wallet and withdraw (in-memory only)
	srcWalletEvents, err := h.store.Load(ctx, cmd.FromWalletID)
	if err != nil {
		return "", err
	}
	fromAgg := wallet.NewAggregate()
	for _, e := range srcWalletEvents {
		raw, err := unmarshalWalletEvent(e)
		if err != nil {
			return "", err
		}
		fromAgg.Apply(raw)
	}
	// Step 3: Load destination wallet and deposit (in-memory only)
	dstWalletEvents, err := h.store.Load(ctx, cmd.ToWalletID)
	if err != nil {
		return "", err
	}
	toAgg := wallet.NewAggregate()
	for _, e := range dstWalletEvents {
		raw, err := unmarshalWalletEvent(e)
		if err != nil {
			return "", err
		}
		toAgg.Apply(raw)
	}

	err = walletservices.TransferMoney(fromAgg, toAgg, cmd)
	if err != nil {
		return "", err
	}

	// Step 4: Create transaction aggregate
	transactionID := uuid.New().String()
	txnAgg, err := transaction.Create(transactionID, cmd.UserID, cmd.FromWalletID, cmd.ToWalletID, cmd.Amount, cmd.Currency, cmd.Reason, now)
	if err != nil {
		return "", err
	}

	// Step 5: Complete transaction
	if err := txnAgg.Complete(now); err != nil {
		return "", err
	}

	// Step 6: Persist all events atomically via AppendMulti
	fromEnvelopes, err := walletEnvelopes(fromAgg.Changes(), cmd.FromWalletID, now)
	if err != nil {
		return "", err
	}
	toEnvs, err := walletEnvelopes(toAgg.Changes(), cmd.ToWalletID, now)
	if err != nil {
		return "", err
	}
	txnEnvelopes, err := txnEnvelopesFn(txnAgg.Changes(), transactionID, now)
	if err != nil {
		return "", err
	}

	batches := []eventstore.AggregateEvents{
		{AggregateID: cmd.FromWalletID, Events: fromEnvelopes, ExpectedVersion: len(srcWalletEvents)},
		{AggregateID: cmd.ToWalletID, Events: toEnvs, ExpectedVersion: len(dstWalletEvents)},
		{AggregateID: transactionID, Events: txnEnvelopes, ExpectedVersion: 0},
	}

	if err := h.store.AppendMulti(ctx, batches); err != nil {
		return "", err
	}

	return transactionID, nil
}

func walletEnvelopes(changes []wallet.DomainEvent, aggregateID string, now time.Time) ([]eventstore.Event, error) {
	envelopes := make([]eventstore.Event, 0, len(changes))
	for _, change := range changes {
		payload, err := json.Marshal(change)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, eventstore.Event{
			EventID:       uuid.New().String(),
			AggregateID:   aggregateID,
			AggregateType: "wallet",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    now,
		})
	}
	return envelopes, nil
}

func txnEnvelopesFn(changes []transaction.DomainEvent, aggregateID string, now time.Time) ([]eventstore.Event, error) {
	envelopes := make([]eventstore.Event, 0, len(changes))
	for _, change := range changes {
		payload, err := json.Marshal(change)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, eventstore.Event{
			EventID:       uuid.New().String(),
			AggregateID:   aggregateID,
			AggregateType: "transaction",
			EventType:     change.EventType(),
			Payload:       payload,
			OccurredAt:    now,
		})
	}
	return envelopes, nil
}

func unmarshalWalletEvent(e eventstore.Event) (any, error) {
	switch e.EventType {
	case wallet.EventTypeWalletAdded:
		var evt wallet.WalletAdded
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case wallet.EventTypeMoneyDeposited:
		var evt wallet.MoneyDeposited
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case wallet.EventTypeMoneyWithdrawn:
		var evt wallet.MoneyWithdrawn
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case wallet.EventTypeWalletDisabled:
		var evt wallet.WalletDisabled
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	default:
		return nil, nil
	}
}
