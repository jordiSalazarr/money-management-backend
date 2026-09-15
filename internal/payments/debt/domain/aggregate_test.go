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

// TestSettlementLifecycle covers the three-step shape a debt settlement now
// has. Requesting one no longer settles it: the money has not moved yet, and
// the reactor may still be refused. See ADR 0002.
func TestSettlementLifecycle(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	settleTime := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)

	newDebt := func(t *testing.T) *debt.Aggregate {
		t.Helper()
		agg, err := debt.Create("debt-1", "user-1", "wallet-1", 1000, "EUR", "IN", "Dinner", now)
		if err != nil {
			t.Fatalf("creating debt: %v", err)
		}
		return agg
	}

	lastEvent := func(agg *debt.Aggregate) string {
		changes := agg.Changes()
		return changes[len(changes)-1].EventType()
	}

	t.Run("requesting a settlement does not settle the debt", func(t *testing.T) {
		agg := newDebt(t)

		if err := agg.RequestSettlement("user-1", settleTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Status() == debt.DebtStatusSettled {
			t.Fatal("the debt must not read settled — no money has moved yet")
		}
		if agg.Status() != debt.DebtStatusSettling {
			t.Errorf("Status = %q, want %q", agg.Status(), debt.DebtStatusSettling)
		}
		if got := lastEvent(agg); got != "DebtSettlementRequested" {
			t.Errorf("EventType = %q, want DebtSettlementRequested", got)
		}
	})

	t.Run("marking it settled closes the settlement", func(t *testing.T) {
		agg := newDebt(t)
		_ = agg.RequestSettlement("user-1", settleTime)

		if err := agg.MarkSettled("user-1", settleTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Status() != debt.DebtStatusSettled {
			t.Errorf("Status = %q, want %q", agg.Status(), debt.DebtStatusSettled)
		}
		if got := lastEvent(agg); got != "DebtSettled" {
			t.Errorf("EventType = %q, want DebtSettled", got)
		}
	})

	t.Run("a failed settlement leaves the debt standing", func(t *testing.T) {
		agg := newDebt(t)
		_ = agg.RequestSettlement("user-1", settleTime)

		if err := agg.FailSettlement("insufficient_funds", settleTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Back to standing rather than a dead end, so the owner can settle it
		// once they have the funds.
		if agg.Status() != debt.DebtStatusCreated {
			t.Errorf("Status = %q, want %q", agg.Status(), debt.DebtStatusCreated)
		}
		if err := agg.RequestSettlement("user-1", settleTime); err != nil {
			t.Errorf("a failed settlement must be retryable, got %v", err)
		}
	})

	t.Run("cannot settle a debt that is already settled", func(t *testing.T) {
		agg := newDebt(t)
		_ = agg.RequestSettlement("user-1", settleTime)
		_ = agg.MarkSettled("user-1", settleTime)

		if err := agg.RequestSettlement("user-1", settleTime); err != debt.ErrDebtAlreadySettled {
			t.Errorf("err = %v, want %v", err, debt.ErrDebtAlreadySettled)
		}
	})

	t.Run("cannot request a settlement while one is in progress", func(t *testing.T) {
		agg := newDebt(t)
		_ = agg.RequestSettlement("user-1", settleTime)

		if err := agg.RequestSettlement("user-1", settleTime); err != debt.ErrSettlementInProgress {
			t.Errorf("err = %v, want %v", err, debt.ErrSettlementInProgress)
		}
	})

	t.Run("cannot mark settled without a settlement in progress", func(t *testing.T) {
		agg := newDebt(t)

		if err := agg.MarkSettled("user-1", settleTime); err != debt.ErrSettlementNotRequested {
			t.Errorf("err = %v, want %v", err, debt.ErrSettlementNotRequested)
		}
	})

	t.Run("missing settled_by", func(t *testing.T) {
		agg := newDebt(t)

		if err := agg.RequestSettlement("", settleTime); err != debt.ErrMissingSettledBy {
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
