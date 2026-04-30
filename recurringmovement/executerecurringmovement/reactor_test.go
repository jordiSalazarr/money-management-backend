package executerecurringmovement_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/recurringmovement"
	"github.com/jordisalazar/money-management/recurringmovement/executerecurringmovement"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/outbox"
	"github.com/jordisalazar/money-management/wallet"
)

func setupStoreWithWallet(t *testing.T, walletID, currency string, balance int) eventstore.Store {
	t.Helper()
	store := eventstore.NewMemoryStore()
	store.RegisterEvent(wallet.EventTypeWalletAdded, func() any { return &wallet.WalletAdded{} })
	store.RegisterEvent(wallet.EventTypeMoneyDeposited, func() any { return &wallet.MoneyDeposited{} })
	store.RegisterEvent(wallet.EventTypeMoneyWithdrawn, func() any { return &wallet.MoneyWithdrawn{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementRegistered, func() any { return &recurringmovement.RecurringMovementRegistered{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementExecuted, func() any { return &recurringmovement.RecurringMovementExecuted{} })

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	evt := wallet.WalletAdded{
		WalletID:      walletID,
		UserID:        "user-1",
		Name:          "Main Wallet",
		InitialAmount: balance,
		Currency:      currency,
		CreatedAt:     now,
	}
	payload, _ := json.Marshal(evt)
	_ = store.Append(context.Background(), walletID, []eventstore.Event{
		{
			EventID:       "evt-wallet-1",
			AggregateID:   walletID,
			AggregateType: "wallet",
			EventType:     wallet.EventTypeWalletAdded,
			Payload:       payload,
			OccurredAt:    now,
		},
	}, 0)
	return store
}

func TestReactor_DepositOnExecution(t *testing.T) {
	store := setupStoreWithWallet(t, "wallet-1", "EUR", 5000)
	handler := executerecurringmovement.NewReactorHandler(store)

	outboxPayload, _ := json.Marshal(executerecurringmovement.RecurringMovementExecutedPayload{
		RecurringMovementID: "rm-1",
		WalletID:            "wallet-1",
		UserID:              "user-1",
		Type:                "deposit",
		Amount:              1000,
		Currency:            "EUR",
		Reason:              "recurring:rm-1",
	})

	entry := outbox.Entry{
		ID:        "outbox-1",
		EventType: recurringmovement.EventTypeRecurringMovementExecuted,
		Payload:   outboxPayload,
		CreatedAt: time.Now().UTC(),
	}

	err := handler(context.Background(), entry)
	if err != nil {
		t.Fatalf("reactor failed: %v", err)
	}

	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 2 {
		t.Fatalf("wallet events = %d, want 2", len(events))
	}
	if events[1].EventType != wallet.EventTypeMoneyDeposited {
		t.Errorf("event type = %q, want %q", events[1].EventType, wallet.EventTypeMoneyDeposited)
	}
}

func TestReactor_WithdrawOnExecution(t *testing.T) {
	store := setupStoreWithWallet(t, "wallet-1", "EUR", 5000)
	handler := executerecurringmovement.NewReactorHandler(store)

	outboxPayload, _ := json.Marshal(executerecurringmovement.RecurringMovementExecutedPayload{
		RecurringMovementID: "rm-2",
		WalletID:            "wallet-1",
		UserID:              "user-1",
		Type:                "withdrawal",
		Amount:              2000,
		Currency:            "EUR",
		Reason:              "recurring:rm-2",
	})

	entry := outbox.Entry{
		ID:        "outbox-2",
		EventType: recurringmovement.EventTypeRecurringMovementExecuted,
		Payload:   outboxPayload,
		CreatedAt: time.Now().UTC(),
	}

	err := handler(context.Background(), entry)
	if err != nil {
		t.Fatalf("reactor failed: %v", err)
	}

	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 2 {
		t.Fatalf("wallet events = %d, want 2", len(events))
	}
	if events[1].EventType != wallet.EventTypeMoneyWithdrawn {
		t.Errorf("event type = %q, want %q", events[1].EventType, wallet.EventTypeMoneyWithdrawn)
	}
}
