package executerecurringmovement_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/slices/executerecurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// seedStore primes an in-memory store with one wallet and one
// recurring-movement aggregate. The reactor under test reads the
// recurring movement to discover its target wallet / amount / direction.
func seedStore(t *testing.T, walletID, rmID, currency, typ string, amount int) eventstore.Store {
	t.Helper()
	store := eventstore.NewMemoryStore()
	store.RegisterEvent(wallet.EventTypeWalletAdded, func() any { return &wallet.WalletAdded{} })
	store.RegisterEvent(wallet.EventTypeMoneyDeposited, func() any { return &wallet.MoneyDeposited{} })
	store.RegisterEvent(wallet.EventTypeMoneyWithdrawn, func() any { return &wallet.MoneyWithdrawn{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementRegistered, func() any { return &recurringmovement.RecurringMovementRegistered{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementExecuted, func() any { return &recurringmovement.RecurringMovementExecuted{} })

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	walletPayload, _ := json.Marshal(wallet.WalletAdded{
		WalletID: walletID, UserID: "user-1", Name: "Main",
		InitialAmount: 5000, Currency: currency, Category: "Cash", CreatedAt: now,
	})
	_ = store.Append(context.Background(), walletID, []eventstore.Event{
		{EventID: "evt-wallet-1", AggregateID: walletID, AggregateType: "wallet",
			EventType: wallet.EventTypeWalletAdded, Payload: walletPayload, OccurredAt: now},
	}, 0)

	rmPayload, _ := json.Marshal(recurringmovement.RecurringMovementRegistered{
		RecurringMovementID: rmID, WalletID: walletID, UserID: "user-1",
		Type: typ, Amount: amount, Currency: currency,
	})
	_ = store.Append(context.Background(), rmID, []eventstore.Event{
		{EventID: "evt-rm-registered", AggregateID: rmID, AggregateType: "recurring_movement",
			EventType: recurringmovement.EventTypeRecurringMovementRegistered, Payload: rmPayload, OccurredAt: now},
	}, 0)
	return store
}

func TestReactor_DepositOnExecution(t *testing.T) {
	store := seedStore(t, "wallet-1", "rm-1", "EUR", "deposit", 1000)
	handler := executerecurringmovement.NewReactorHandler(store)

	err := handler(context.Background(),
		recurringmovement.RecurringMovementExecuted{
			RecurringMovementID: "rm-1",
			ExecutedForDate:     time.Now().UTC(),
			ExecutedAt:          time.Now().UTC(),
		},
		eventstore.EventMetadata{EventID: "evt-rm-executed-1"})
	if err != nil {
		t.Fatalf("reactor: %v", err)
	}

	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 2 || events[1].EventType != wallet.EventTypeMoneyDeposited {
		t.Fatalf("expected MoneyDeposited as 2nd event")
	}
	var dep wallet.MoneyDeposited
	json.Unmarshal(events[1].Payload, &dep)
	if dep.IdempotencyKey != "evt-rm-executed-1" {
		t.Errorf("idempotency key = %q, want evt-rm-executed-1", dep.IdempotencyKey)
	}
}

func TestReactor_WithdrawOnExecution(t *testing.T) {
	store := seedStore(t, "wallet-1", "rm-2", "EUR", "withdrawal", 2000)
	handler := executerecurringmovement.NewReactorHandler(store)

	err := handler(context.Background(),
		recurringmovement.RecurringMovementExecuted{
			RecurringMovementID: "rm-2",
			ExecutedForDate:     time.Now().UTC(),
			ExecutedAt:          time.Now().UTC(),
		},
		eventstore.EventMetadata{EventID: "evt-rm-executed-2"})
	if err != nil {
		t.Fatalf("reactor: %v", err)
	}

	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 2 || events[1].EventType != wallet.EventTypeMoneyWithdrawn {
		t.Fatalf("expected MoneyWithdrawn as 2nd event")
	}
}

func TestReactor_Idempotent(t *testing.T) {
	store := seedStore(t, "wallet-1", "rm-3", "EUR", "deposit", 1500)
	handler := executerecurringmovement.NewReactorHandler(store)
	e := recurringmovement.RecurringMovementExecuted{
		RecurringMovementID: "rm-3",
		ExecutedForDate:     time.Now().UTC(),
		ExecutedAt:          time.Now().UTC(),
	}
	meta := eventstore.EventMetadata{EventID: "evt-once"}

	if err := handler(context.Background(), e, meta); err != nil {
		t.Fatalf("first call: %v", err)
	}
	err := handler(context.Background(), e, meta)
	if !errors.Is(err, wallet.ErrAlreadyProcessed) {
		t.Fatalf("got %v, want ErrAlreadyProcessed", err)
	}
	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 2 {
		t.Errorf("wallet events = %d, want 2 (no second deposit on retry)", len(events))
	}
}
