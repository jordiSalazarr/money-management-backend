package settledebt_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt"
	"github.com/jordisalazar/money-management/internal/payments/debt/settledebt"
	"github.com/jordisalazar/money-management/internal/payments/wallet"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// seedStore primes an in-memory event store with one wallet (at the
// given balance) and one outstanding debt against it. The reactor under
// test loads the debt to read its direction/amount/currency, then issues
// the matching deposit/withdraw command.
func seedStore(t *testing.T, walletID, debtID, currency, direction string, debtAmount int) eventstore.Store {
	t.Helper()
	store := eventstore.NewMemoryStore()
	store.RegisterEvent(wallet.EventTypeWalletAdded, func() any { return &wallet.WalletAdded{} })
	store.RegisterEvent(wallet.EventTypeMoneyDeposited, func() any { return &wallet.MoneyDeposited{} })
	store.RegisterEvent(wallet.EventTypeMoneyWithdrawn, func() any { return &wallet.MoneyWithdrawn{} })
	store.RegisterEvent(debt.EventTypeDebtRecorded, func() any { return &debt.DebtRecorded{} })
	store.RegisterEvent(debt.EventTypeDebtSettled, func() any { return &debt.DebtSettled{} })

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	walletPayload, _ := json.Marshal(wallet.WalletAdded{
		WalletID: walletID, UserID: "user-1", Name: "Main", InitialAmount: 5000,
		Currency: currency, Category: "Cash", CreatedAt: now,
	})
	if err := store.Append(context.Background(), walletID, []eventstore.Event{
		{EventID: "evt-wallet-1", AggregateID: walletID, AggregateType: "wallet",
			EventType: wallet.EventTypeWalletAdded, Payload: walletPayload, OccurredAt: now},
	}, 0); err != nil {
		t.Fatalf("seed wallet: %v", err)
	}

	debtPayload, _ := json.Marshal(debt.DebtRecorded{
		DebtID: debtID, WalletID: walletID, OwnerID: "user-1",
		Amount: debtAmount, Currency: currency, Direction: direction,
		Reason: "test", CreatedAt: now,
	})
	if err := store.Append(context.Background(), debtID, []eventstore.Event{
		{EventID: "evt-debt-recorded", AggregateID: debtID, AggregateType: "debt",
			EventType: debt.EventTypeDebtRecorded, Payload: debtPayload, OccurredAt: now},
	}, 0); err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	return store
}

func TestReactor_DepositOnSettleIN(t *testing.T) {
	store := seedStore(t, "wallet-1", "debt-1", "EUR", "IN", 1000)
	handler := settledebt.NewReactorHandler(store)

	err := handler(context.Background(),
		debt.DebtSettled{DebtID: "debt-1", SettledBy: "user-1", SettledAt: time.Now().UTC()},
		eventstore.EventMetadata{EventID: "evt-debt-settled-1"})
	if err != nil {
		t.Fatalf("reactor: %v", err)
	}

	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 2 {
		t.Fatalf("wallet events = %d, want 2", len(events))
	}
	if events[1].EventType != wallet.EventTypeMoneyDeposited {
		t.Errorf("type = %q, want MoneyDeposited", events[1].EventType)
	}

	var dep wallet.MoneyDeposited
	json.Unmarshal(events[1].Payload, &dep)
	if dep.Amount != 1000 {
		t.Errorf("amount = %d, want 1000", dep.Amount)
	}
	if dep.IdempotencyKey != "evt-debt-settled-1" {
		t.Errorf("idempotency key = %q, want evt-debt-settled-1", dep.IdempotencyKey)
	}
	if dep.Reason != "debt_settlement:debt-1" {
		t.Errorf("reason = %q", dep.Reason)
	}
}

func TestReactor_WithdrawOnSettleOUT(t *testing.T) {
	store := seedStore(t, "wallet-1", "debt-2", "EUR", "OUT", 2000)
	handler := settledebt.NewReactorHandler(store)

	err := handler(context.Background(),
		debt.DebtSettled{DebtID: "debt-2", SettledBy: "user-1", SettledAt: time.Now().UTC()},
		eventstore.EventMetadata{EventID: "evt-debt-settled-2"})
	if err != nil {
		t.Fatalf("reactor: %v", err)
	}

	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 2 || events[1].EventType != wallet.EventTypeMoneyWithdrawn {
		t.Fatalf("expected MoneyWithdrawn, got %+v", events)
	}
}

func TestReactor_Idempotent(t *testing.T) {
	store := seedStore(t, "wallet-1", "debt-3", "EUR", "IN", 1500)
	handler := settledebt.NewReactorHandler(store)
	meta := eventstore.EventMetadata{EventID: "evt-once"}
	e := debt.DebtSettled{DebtID: "debt-3", SettledBy: "user-1", SettledAt: time.Now().UTC()}

	if err := handler(context.Background(), e, meta); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call with the same metadata must not move money again.
	err := handler(context.Background(), e, meta)
	if !errors.Is(err, wallet.ErrAlreadyProcessed) {
		t.Fatalf("got %v, want ErrAlreadyProcessed", err)
	}
	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 2 {
		t.Errorf("wallet events = %d, want 2 (idempotent retry must not append a second deposit)", len(events))
	}
}

func TestReactor_InsufficientFunds(t *testing.T) {
	store := seedStore(t, "wallet-1", "debt-4", "EUR", "OUT", 999999)
	handler := settledebt.NewReactorHandler(store)

	err := handler(context.Background(),
		debt.DebtSettled{DebtID: "debt-4", SettledBy: "user-1", SettledAt: time.Now().UTC()},
		eventstore.EventMetadata{EventID: "evt-debt-settled-4"})
	if err == nil {
		t.Fatal("expected insufficient-funds error")
	}
	events, _ := store.Load(context.Background(), "wallet-1")
	if len(events) != 1 {
		t.Errorf("wallet events = %d, want 1 (no withdrawal should have committed)", len(events))
	}
}

func TestReactor_DebtNotFound(t *testing.T) {
	store := eventstore.NewMemoryStore()
	store.RegisterEvent(debt.EventTypeDebtRecorded, func() any { return &debt.DebtRecorded{} })
	store.RegisterEvent(debt.EventTypeDebtSettled, func() any { return &debt.DebtSettled{} })
	handler := settledebt.NewReactorHandler(store)

	err := handler(context.Background(),
		debt.DebtSettled{DebtID: "ghost", SettledBy: "user-1", SettledAt: time.Now().UTC()},
		eventstore.EventMetadata{EventID: "evt-x"})
	if err == nil {
		t.Fatal("expected error loading missing debt")
	}
}
