package debt_test

import (
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
)

func TestCreate(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	t.Run("creates debt with valid inputs", func(t *testing.T) {
		agg, err := debt.Create("debt-1", "user-1", "wallet-1", 1000, "EUR", "IN", "Dinner split", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.ID() != "debt-1" {
			t.Errorf("ID = %q, want %q", agg.ID(), "debt-1")
		}
		if agg.OwnerID() != "user-1" {
			t.Errorf("OwnerID = %q, want %q", agg.OwnerID(), "user-1")
		}
		if agg.WalletID() != "wallet-1" {
			t.Errorf("WalletID = %q, want %q", agg.WalletID(), "wallet-1")
		}
		if agg.Amount() != 1000 {
			t.Errorf("Amount = %d, want %d", agg.Amount(), 1000)
		}
		if agg.Currency() != "EUR" {
			t.Errorf("Currency = %q, want %q", agg.Currency(), "EUR")
		}
		if agg.Direction() != "IN" {
			t.Errorf("Direction = %q, want %q", agg.Direction(), "IN")
		}
		if agg.Reason() != "Dinner split" {
			t.Errorf("Reason = %q, want %q", agg.Reason(), "Dinner split")
		}
		if agg.Version() != 1 {
			t.Errorf("Version = %d, want %d", agg.Version(), 1)
		}
		if len(agg.Changes()) != 1 {
			t.Fatalf("Changes = %d, want 1", len(agg.Changes()))
		}
		if agg.Changes()[0].EventType() != "DebtRecorded" {
			t.Errorf("EventType = %q, want %q", agg.Changes()[0].EventType(), "DebtRecorded")
		}
	})

	tests := []struct {
		name      string
		debtID    string
		ownerID   string
		walletID  string
		amount    int
		currency  string
		direction string
		reason    string
		wantErr   error
	}{
		{
			name:   "missing owner_id",
			debtID: "d-1", ownerID: "", walletID: "w-1", amount: 1000, currency: "EUR", direction: "IN", reason: "test",
			wantErr: debt.ErrMissingOwnerID,
		},
		{
			name:   "missing wallet_id",
			debtID: "d-1", ownerID: "u-1", walletID: "", amount: 1000, currency: "EUR", direction: "IN", reason: "test",
			wantErr: debt.ErrMissingWalletID,
		},
		{
			name:   "zero amount",
			debtID: "d-1", ownerID: "u-1", walletID: "w-1", amount: 0, currency: "EUR", direction: "IN", reason: "test",
			wantErr: debt.ErrInvalidAmount,
		},
		{
			name:   "negative amount",
			debtID: "d-1", ownerID: "u-1", walletID: "w-1", amount: -100, currency: "EUR", direction: "IN", reason: "test",
			wantErr: debt.ErrInvalidAmount,
		},
		{
			name:   "amount too high",
			debtID: "d-1", ownerID: "u-1", walletID: "w-1", amount: 10_000_001, currency: "EUR", direction: "IN", reason: "test",
			wantErr: debt.ErrInvalidAmount,
		},
		{
			name:   "invalid currency",
			debtID: "d-1", ownerID: "u-1", walletID: "w-1", amount: 1000, currency: "GBP", direction: "IN", reason: "test",
			wantErr: debt.ErrInvalidCurrency,
		},
		{
			name:   "invalid direction",
			debtID: "d-1", ownerID: "u-1", walletID: "w-1", amount: 1000, currency: "EUR", direction: "BOTH", reason: "test",
			wantErr: debt.ErrInvalidDirection,
		},
		{
			name:   "missing reason",
			debtID: "d-1", ownerID: "u-1", walletID: "w-1", amount: 1000, currency: "EUR", direction: "IN", reason: "",
			wantErr: debt.ErrMissingReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := debt.Create(tt.debtID, tt.ownerID, tt.walletID, tt.amount, tt.currency, tt.direction, tt.reason, now)
			if err != tt.wantErr {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSettle(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	settleTime := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)

	t.Run("settles a created debt", func(t *testing.T) {
		agg, _ := debt.Create("debt-1", "user-1", "wallet-1", 1000, "EUR", "IN", "Dinner", now)
		// Clear creation changes
		_ = agg.Changes()

		err := agg.Settle("user-1", settleTime)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Status() != debt.DebtStatusSettled {
			t.Errorf("Status = %q, want %q", agg.Status(), debt.DebtStatusSettled)
		}
		changes := agg.Changes()
		// Changes includes DebtRecorded + DebtSettled
		found := false
		for _, c := range changes {
			if c.EventType() == "DebtSettled" {
				found = true
			}
		}
		if !found {
			t.Error("expected DebtSettled event in changes")
		}
	})

	t.Run("cannot settle twice", func(t *testing.T) {
		agg, _ := debt.Create("debt-1", "user-1", "wallet-1", 1000, "EUR", "IN", "Dinner", now)
		_ = agg.Settle("user-1", settleTime)

		err := agg.Settle("user-1", settleTime)
		if err != debt.ErrDebtAlreadySettled {
			t.Errorf("err = %v, want %v", err, debt.ErrDebtAlreadySettled)
		}
	})

	t.Run("missing settled_by", func(t *testing.T) {
		agg, _ := debt.Create("debt-1", "user-1", "wallet-1", 1000, "EUR", "IN", "Dinner", now)

		err := agg.Settle("", settleTime)
		if err != debt.ErrMissingSettledBy {
			t.Errorf("err = %v, want %v", err, debt.ErrMissingSettledBy)
		}
	})
}

func TestAggregate_Apply_DebtRecorded(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	agg := debt.NewAggregate()
	agg.Apply(debt.DebtRecorded{
		DebtID:    "debt-1",
		OwnerID:   "user-1",
		WalletID:  "wallet-1",
		Amount:    1000,
		Currency:  "EUR",
		Direction: "IN",
		Reason:    "Dinner split",
		CreatedAt: now,
	})

	if agg.ID() != "debt-1" {
		t.Errorf("ID = %q, want %q", agg.ID(), "debt-1")
	}
	if agg.Amount() != 1000 {
		t.Errorf("Amount = %d, want %d", agg.Amount(), 1000)
	}
	if agg.Version() != 1 {
		t.Errorf("Version = %d, want %d", agg.Version(), 1)
	}
	if len(agg.Changes()) != 0 {
		t.Errorf("Changes = %d, want 0 (Apply should not track changes)", len(agg.Changes()))
	}
}
