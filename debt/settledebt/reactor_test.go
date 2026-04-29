package settledebt_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/debt"
	"github.com/jordisalazar/money-management/debt/settledebt"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/outbox"
	"github.com/jordisalazar/money-management/wallet"
)

func setupStoreWithWallet(t *testing.T, walletID string, currency string) eventstore.Store {
	t.Helper()
	store := eventstore.NewMemoryStore()
	store.RegisterEvent(wallet.EventTypeWalletAdded, func() any { return &wallet.WalletAdded{} })
	store.RegisterEvent(wallet.EventTypeMoneyDeposited, func() any { return &wallet.MoneyDeposited{} })
	store.RegisterEvent(wallet.EventTypeMoneyWithdrawn, func() any { return &wallet.MoneyWithdrawn{} })
	store.RegisterEvent(debt.EventTypeDebtRecorded, func() any { return &debt.DebtRecorded{} })
	store.RegisterEvent(debt.EventTypeDebtSettled, func() any { return &debt.DebtSettled{} })

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	evt := wallet.WalletAdded{
		WalletID:      walletID,
		UserID:        "user-1",
		Name:          "Main Wallet",
		Description:   "",
		InitialAmount: 5000,
		Currency:      currency,
		Category:      "",
		CreatedAt:     now,
	}
	payload, _ := json.Marshal(evt)
	err := store.Append(context.Background(), walletID, []eventstore.Event{
		{
			EventID:       "evt-wallet-1",
			AggregateID:   walletID,
			AggregateType: "wallet",
			EventType:     wallet.EventTypeWalletAdded,
			Payload:       payload,
			OccurredAt:    now,
		},
	}, 0)
	if err != nil {
		t.Fatalf("failed to seed wallet: %v", err)
	}
	return store
}

func TestReactor_DepositOnSettleIN(t *testing.T) {
	store := setupStoreWithWallet(t, "wallet-1", "EUR")
	handler := settledebt.NewReactorHandler(store)

	outboxPayload, _ := json.Marshal(settledebt.DebtSettledPayload{
		DebtID:    "debt-1",
		WalletID:  "wallet-1",
		Amount:    1000,
		Currency:  "EUR",
		Direction: "IN",
		SettledBy: "user-1",
	})

	entry := outbox.Entry{
		ID:        "outbox-1",
		EventType: debt.EventTypeDebtSettled,
		Payload:   outboxPayload,
		CreatedAt: time.Now().UTC(),
	}

	err := handler(context.Background(), entry)
	if err != nil {
		t.Fatalf("reactor failed: %v", err)
	}

	// Verify wallet now has 2 events (WalletAdded + MoneyDeposited)
	events, err := store.Load(context.Background(), "wallet-1")
	if err != nil {
		t.Fatalf("failed to load wallet events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("wallet events = %d, want 2", len(events))
	}
	if events[1].EventType != wallet.EventTypeMoneyDeposited {
		t.Errorf("event type = %q, want %q", events[1].EventType, wallet.EventTypeMoneyDeposited)
	}

	// Verify deposit amount
	var deposited wallet.MoneyDeposited
	if err := json.Unmarshal(events[1].Payload, &deposited); err != nil {
		t.Fatalf("failed to unmarshal deposit event: %v", err)
	}
	if deposited.Amount != 1000 {
		t.Errorf("deposit amount = %d, want 1000", deposited.Amount)
	}
	if deposited.Reason != "debt_settlement:debt-1" {
		t.Errorf("deposit reason = %q, want %q", deposited.Reason, "debt_settlement:debt-1")
	}
}

func TestReactor_WithdrawOnSettleOUT(t *testing.T) {
	store := setupStoreWithWallet(t, "wallet-1", "EUR")
	handler := settledebt.NewReactorHandler(store)

	outboxPayload, _ := json.Marshal(settledebt.DebtSettledPayload{
		DebtID:    "debt-2",
		WalletID:  "wallet-1",
		Amount:    2000,
		Currency:  "EUR",
		Direction: "OUT",
		SettledBy: "user-1",
	})

	entry := outbox.Entry{
		ID:        "outbox-2",
		EventType: debt.EventTypeDebtSettled,
		Payload:   outboxPayload,
		CreatedAt: time.Now().UTC(),
	}

	err := handler(context.Background(), entry)
	if err != nil {
		t.Fatalf("reactor failed: %v", err)
	}

	// Verify wallet now has 2 events (WalletAdded + MoneyWithdrawn)
	events, err := store.Load(context.Background(), "wallet-1")
	if err != nil {
		t.Fatalf("failed to load wallet events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("wallet events = %d, want 2", len(events))
	}
	if events[1].EventType != wallet.EventTypeMoneyWithdrawn {
		t.Errorf("event type = %q, want %q", events[1].EventType, wallet.EventTypeMoneyWithdrawn)
	}

	var withdrawn wallet.MoneyWithdrawn
	if err := json.Unmarshal(events[1].Payload, &withdrawn); err != nil {
		t.Fatalf("failed to unmarshal withdraw event: %v", err)
	}
	if withdrawn.Amount != 2000 {
		t.Errorf("withdraw amount = %d, want 2000", withdrawn.Amount)
	}
	if withdrawn.Reason != "debt_settlement:debt-2" {
		t.Errorf("withdraw reason = %q, want %q", withdrawn.Reason, "debt_settlement:debt-2")
	}
}

func TestReactor_InsufficientFunds(t *testing.T) {
	store := setupStoreWithWallet(t, "wallet-1", "EUR") // balance = 5000
	handler := settledebt.NewReactorHandler(store)

	outboxPayload, _ := json.Marshal(settledebt.DebtSettledPayload{
		DebtID:    "debt-3",
		WalletID:  "wallet-1",
		Amount:    999999, // more than balance
		Currency:  "EUR",
		Direction: "OUT",
		SettledBy: "user-1",
	})

	entry := outbox.Entry{
		ID:        "outbox-3",
		EventType: debt.EventTypeDebtSettled,
		Payload:   outboxPayload,
		CreatedAt: time.Now().UTC(),
	}

	err := handler(context.Background(), entry)
	if err == nil {
		t.Fatal("expected error for insufficient funds, got nil")
	}

	// Wallet should still only have 1 event (WalletAdded)
	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 1 {
		t.Errorf("wallet events = %d, want 1 (no withdrawal should have happened)", len(events))
	}
}

func TestReactor_WalletNotFound(t *testing.T) {
	store := eventstore.NewMemoryStore()
	store.RegisterEvent(wallet.EventTypeWalletAdded, func() any { return &wallet.WalletAdded{} })
	store.RegisterEvent(wallet.EventTypeMoneyDeposited, func() any { return &wallet.MoneyDeposited{} })

	handler := settledebt.NewReactorHandler(store)

	outboxPayload, _ := json.Marshal(settledebt.DebtSettledPayload{
		DebtID:    "debt-4",
		WalletID:  "nonexistent",
		Amount:    1000,
		Currency:  "EUR",
		Direction: "IN",
		SettledBy: "user-1",
	})

	entry := outbox.Entry{
		ID:        "outbox-4",
		EventType: debt.EventTypeDebtSettled,
		Payload:   outboxPayload,
		CreatedAt: time.Now().UTC(),
	}

	err := handler(context.Background(), entry)
	if err == nil {
		t.Fatal("expected error for wallet not found, got nil")
	}
}
