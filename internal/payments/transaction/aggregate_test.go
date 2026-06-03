package transaction_test

import (
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/transaction"
)

func TestCreate(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

	t.Run("creates transaction successfully", func(t *testing.T) {
		agg, err := transaction.Create("txn-1", "user-1", "wallet-a", "wallet-b", 5000, "EUR", "Savings to cash", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.ID() != "txn-1" {
			t.Errorf("ID = %q, want %q", agg.ID(), "txn-1")
		}
		if agg.Status() != "CREATED" {
			t.Errorf("Status = %q, want %q", agg.Status(), "CREATED")
		}
		if agg.Version() != 1 {
			t.Errorf("Version = %d, want %d", agg.Version(), 1)
		}
		if len(agg.Changes()) != 1 {
			t.Fatalf("Changes = %d, want 1", len(agg.Changes()))
		}
		if agg.Changes()[0].EventType() != "TransactionCreated" {
			t.Errorf("EventType = %q, want %q", agg.Changes()[0].EventType(), "TransactionCreated")
		}
	})

	t.Run("creates transaction without reason", func(t *testing.T) {
		agg, err := transaction.Create("txn-2", "user-1", "wallet-a", "wallet-b", 1000, "EUR", "", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.ID() != "txn-2" {
			t.Errorf("ID = %q, want %q", agg.ID(), "txn-2")
		}
	})
}

func TestComplete(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	completeTime := time.Date(2026, 4, 25, 12, 0, 1, 0, time.UTC)

	t.Run("completes transaction successfully", func(t *testing.T) {
		agg, _ := transaction.Create("txn-1", "user-1", "wallet-a", "wallet-b", 5000, "EUR", "reason", now)

		err := agg.Complete(completeTime)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Status() != "COMPLETED" {
			t.Errorf("Status = %q, want %q", agg.Status(), "COMPLETED")
		}
		if agg.Version() != 2 {
			t.Errorf("Version = %d, want %d", agg.Version(), 2)
		}
		if len(agg.Changes()) != 2 {
			t.Fatalf("Changes = %d, want 2", len(agg.Changes()))
		}
		if agg.Changes()[1].EventType() != "TransactionCompleted" {
			t.Errorf("EventType = %q, want %q", agg.Changes()[1].EventType(), "TransactionCompleted")
		}
	})

	t.Run("cannot complete already completed transaction", func(t *testing.T) {
		agg, _ := transaction.Create("txn-1", "user-1", "wallet-a", "wallet-b", 5000, "EUR", "reason", now)
		_ = agg.Complete(completeTime)

		err := agg.Complete(completeTime)
		if err != transaction.ErrAlreadyCompleted {
			t.Errorf("err = %v, want %v", err, transaction.ErrAlreadyCompleted)
		}
	})
}

func TestAggregate_Apply_TransactionCreated(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

	agg := transaction.NewAggregate()
	agg.Apply(transaction.TransactionCreated{
		TransactionID: "txn-1",
		UserID:        "user-1",
		FromWalletID:  "wallet-a",
		ToWalletID:    "wallet-b",
		Amount:        5000,
		Currency:      "EUR",
		Reason:        "test",
		CreatedAt:     now,
	})

	if agg.ID() != "txn-1" {
		t.Errorf("ID = %q, want %q", agg.ID(), "txn-1")
	}
	if agg.Status() != "CREATED" {
		t.Errorf("Status = %q, want %q", agg.Status(), "CREATED")
	}
	if agg.Version() != 1 {
		t.Errorf("Version = %d, want %d", agg.Version(), 1)
	}
	if len(agg.Changes()) != 0 {
		t.Errorf("Changes = %d, want 0 (Apply should not track changes)", len(agg.Changes()))
	}
}
