package recurringmovement_test

import (
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/recurringmovement"
)

func TestCreate(t *testing.T) {
	today := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	startDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)

	t.Run("creates recurring movement with valid inputs", func(t *testing.T) {
		agg, err := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "MONTHLY", "Salary",
			startDate, endDate, now, today,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.ID() != "rm-1" {
			t.Errorf("ID = %q, want %q", agg.ID(), "rm-1")
		}
		if agg.WalletID() != "wallet-1" {
			t.Errorf("WalletID = %q, want %q", agg.WalletID(), "wallet-1")
		}
		if agg.Type() != "deposit" {
			t.Errorf("Type = %q, want %q", agg.Type(), "deposit")
		}
		if agg.Amount() != 1000 {
			t.Errorf("Amount = %d, want %d", agg.Amount(), 1000)
		}
		if agg.Frequency() != "MONTHLY" {
			t.Errorf("Frequency = %q, want %q", agg.Frequency(), "MONTHLY")
		}
		if agg.Version() != 1 {
			t.Errorf("Version = %d, want %d", agg.Version(), 1)
		}
		if len(agg.Changes()) != 1 {
			t.Fatalf("Changes = %d, want 1", len(agg.Changes()))
		}
		if agg.Changes()[0].EventType() != "RecurringMovementRegistered" {
			t.Errorf("EventType = %q, want %q", agg.Changes()[0].EventType(), "RecurringMovementRegistered")
		}
	})

	t.Run("creates without end_date", func(t *testing.T) {
		agg, err := recurringmovement.Create(
			"rm-2", "wallet-1", "user-1", "withdrawal",
			500, "USD", "WEEKLY", "Savings",
			startDate, time.Time{}, now, today,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !agg.EndDate().IsZero() {
			t.Errorf("EndDate should be zero, got %v", agg.EndDate())
		}
	})

	tests := []struct {
		name      string
		walletID  string
		userID    string
		typ       string
		amount    int
		currency  string
		frequency string
		reason    string
		start     time.Time
		end       time.Time
		wantErr   error
	}{
		{
			name: "missing wallet_id",
			walletID: "", userID: "u-1", typ: "deposit", amount: 1000, currency: "EUR", frequency: "MONTHLY", reason: "test", start: startDate,
			wantErr: recurringmovement.ErrMissingWalletID,
		},
		{
			name: "missing user_id",
			walletID: "w-1", userID: "", typ: "deposit", amount: 1000, currency: "EUR", frequency: "MONTHLY", reason: "test", start: startDate,
			wantErr: recurringmovement.ErrMissingUserID,
		},
		{
			name: "invalid type",
			walletID: "w-1", userID: "u-1", typ: "transfer", amount: 1000, currency: "EUR", frequency: "MONTHLY", reason: "test", start: startDate,
			wantErr: recurringmovement.ErrInvalidType,
		},
		{
			name: "zero amount",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: 0, currency: "EUR", frequency: "MONTHLY", reason: "test", start: startDate,
			wantErr: recurringmovement.ErrInvalidAmount,
		},
		{
			name: "negative amount",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: -100, currency: "EUR", frequency: "MONTHLY", reason: "test", start: startDate,
			wantErr: recurringmovement.ErrInvalidAmount,
		},
		{
			name: "amount too high",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: 10_000_001, currency: "EUR", frequency: "MONTHLY", reason: "test", start: startDate,
			wantErr: recurringmovement.ErrInvalidAmount,
		},
		{
			name: "invalid currency",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: 1000, currency: "GBP", frequency: "MONTHLY", reason: "test", start: startDate,
			wantErr: recurringmovement.ErrInvalidCurrency,
		},
		{
			name: "invalid frequency",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: 1000, currency: "EUR", frequency: "YEARLY", reason: "test", start: startDate,
			wantErr: recurringmovement.ErrInvalidFrequency,
		},
		{
			name: "missing reason",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: 1000, currency: "EUR", frequency: "MONTHLY", reason: "", start: startDate,
			wantErr: recurringmovement.ErrMissingReason,
		},
		{
			name: "start_date in the past",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: 1000, currency: "EUR", frequency: "MONTHLY", reason: "test",
			start: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
			wantErr: recurringmovement.ErrInvalidStartDate,
		},
		{
			name: "end_date before start_date",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: 1000, currency: "EUR", frequency: "MONTHLY", reason: "test",
			start: startDate, end: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			wantErr: recurringmovement.ErrInvalidEndDate,
		},
		{
			name: "end_date equal to start_date",
			walletID: "w-1", userID: "u-1", typ: "deposit", amount: 1000, currency: "EUR", frequency: "MONTHLY", reason: "test",
			start: startDate, end: startDate,
			wantErr: recurringmovement.ErrInvalidEndDate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := recurringmovement.Create(
				"rm-test", tt.walletID, tt.userID, tt.typ,
				tt.amount, tt.currency, tt.frequency, tt.reason,
				tt.start, tt.end, now, today,
			)
			if err != tt.wantErr {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestExecute(t *testing.T) {
	today := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	startDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	t.Run("executes active recurring movement", func(t *testing.T) {
		agg, _ := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "MONTHLY", "Salary",
			startDate, time.Time{}, now, today,
		)

		err := agg.Execute(today, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.LastExecutedDate() != today {
			t.Errorf("LastExecutedDate = %v, want %v", agg.LastExecutedDate(), today)
		}
		found := false
		for _, c := range agg.Changes() {
			if c.EventType() == "RecurringMovementExecuted" {
				found = true
			}
		}
		if !found {
			t.Error("expected RecurringMovementExecuted event in changes")
		}
	})

	t.Run("cannot execute twice for same date", func(t *testing.T) {
		agg, _ := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "MONTHLY", "Salary",
			startDate, time.Time{}, now, today,
		)
		_ = agg.Execute(today, now)

		err := agg.Execute(today, now)
		if err != recurringmovement.ErrAlreadyExecutedToday {
			t.Errorf("err = %v, want %v", err, recurringmovement.ErrAlreadyExecutedToday)
		}
	})

	t.Run("can execute for next day after previous execution", func(t *testing.T) {
		agg, _ := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "DAILY", "Salary",
			startDate, time.Time{}, now, today,
		)
		_ = agg.Execute(today, now)

		tomorrow := today.AddDate(0, 0, 1)
		err := agg.Execute(tomorrow, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.LastExecutedDate() != tomorrow {
			t.Errorf("LastExecutedDate = %v, want %v", agg.LastExecutedDate(), tomorrow)
		}
	})
}

func TestStop(t *testing.T) {
	today := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	startDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	t.Run("stops active recurring movement", func(t *testing.T) {
		agg, _ := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "MONTHLY", "Salary",
			startDate, time.Time{}, now, today,
		)

		err := agg.Stop("user-1", "no longer needed", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Active() {
			t.Error("expected active = false after stop")
		}
		found := false
		for _, c := range agg.Changes() {
			if c.EventType() == "RecurringMovementStopped" {
				found = true
			}
		}
		if !found {
			t.Error("expected RecurringMovementStopped event in changes")
		}
	})

	t.Run("stops with empty reason", func(t *testing.T) {
		agg, _ := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "MONTHLY", "Salary",
			startDate, time.Time{}, now, today,
		)

		err := agg.Stop("user-1", "", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Active() {
			t.Error("expected active = false after stop")
		}
	})

	t.Run("cannot stop inactive recurring movement", func(t *testing.T) {
		agg, _ := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "MONTHLY", "Salary",
			startDate, time.Time{}, now, today,
		)
		_ = agg.Stop("user-1", "", now)

		err := agg.Stop("user-1", "", now)
		if err != recurringmovement.ErrNotActive {
			t.Errorf("err = %v, want %v", err, recurringmovement.ErrNotActive)
		}
	})

	t.Run("cannot stop if not owner", func(t *testing.T) {
		agg, _ := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "MONTHLY", "Salary",
			startDate, time.Time{}, now, today,
		)

		err := agg.Stop("other-user", "stealing", now)
		if err != recurringmovement.ErrNotOwner {
			t.Errorf("err = %v, want %v", err, recurringmovement.ErrNotOwner)
		}
		if !agg.Active() {
			t.Error("expected active = true, stop should have been rejected")
		}
	})

	t.Run("cannot execute after stop", func(t *testing.T) {
		agg, _ := recurringmovement.Create(
			"rm-1", "wallet-1", "user-1", "deposit",
			1000, "EUR", "MONTHLY", "Salary",
			startDate, time.Time{}, now, today,
		)
		_ = agg.Stop("user-1", "", now)

		err := agg.Execute(today, now)
		if err != recurringmovement.ErrNotActive {
			t.Errorf("err = %v, want %v", err, recurringmovement.ErrNotActive)
		}
	})
}

func TestAggregate_Apply_RecurringMovementRegistered(t *testing.T) {
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	startDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	agg := recurringmovement.NewAggregate()
	agg.Apply(recurringmovement.RecurringMovementRegistered{
		RecurringMovementID: "rm-1",
		WalletID:            "wallet-1",
		UserID:              "user-1",
		Type:                "deposit",
		Amount:              1000,
		Currency:            "EUR",
		Frequency:           "MONTHLY",
		Reason:              "Salary",
		StartDate:           startDate,
		RegisteredAt:        now,
	})

	if agg.ID() != "rm-1" {
		t.Errorf("ID = %q, want %q", agg.ID(), "rm-1")
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
