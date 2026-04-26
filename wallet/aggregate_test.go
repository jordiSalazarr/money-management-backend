package wallet_test

import (
	"testing"
	"time"

	"github.com/jordisalazar/money-management/wallet"
)

func TestCreate(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	t.Run("creates wallet with valid inputs", func(t *testing.T) {
		agg, err := wallet.Create("wallet-1", "user-1", "My Savings", "Main savings", 50000, "EUR", "Saving", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.ID() != "wallet-1" {
			t.Errorf("ID = %q, want %q", agg.ID(), "wallet-1")
		}
		if agg.Balance() != 50000 {
			t.Errorf("Balance = %d, want %d", agg.Balance(), 50000)
		}
		if agg.Currency() != "EUR" {
			t.Errorf("Currency = %q, want %q", agg.Currency(), "EUR")
		}
		if agg.Category() != "Saving" {
			t.Errorf("Category = %q, want %q", agg.Category(), "Saving")
		}
		if agg.Version() != 1 {
			t.Errorf("Version = %d, want %d", agg.Version(), 1)
		}
		if len(agg.Changes()) != 1 {
			t.Fatalf("Changes = %d, want 1", len(agg.Changes()))
		}
		if agg.Changes()[0].EventType() != "WalletAdded" {
			t.Errorf("EventType = %q, want %q", agg.Changes()[0].EventType(), "WalletAdded")
		}
	})

	t.Run("creates wallet with zero initial amount", func(t *testing.T) {
		agg, err := wallet.Create("wallet-2", "user-1", "Empty Wallet", "", 0, "USD", "Cash", now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Balance() != 0 {
			t.Errorf("Balance = %d, want %d", agg.Balance(), 0)
		}
	})

	tests := []struct {
		name          string
		walletID      string
		userID        string
		walletName    string
		description   string
		initialAmount int
		currency      string
		category      string
		wantErr       error
	}{
		{
			name:     "missing user_id",
			walletID: "w-1", userID: "", walletName: "Test", initialAmount: 0, currency: "EUR", category: "Saving",
			wantErr: wallet.ErrMissingUserID,
		},
		{
			name:     "missing name",
			walletID: "w-1", userID: "u-1", walletName: "", initialAmount: 0, currency: "EUR", category: "Saving",
			wantErr: wallet.ErrMissingName,
		},
		{
			name:     "negative initial amount",
			walletID: "w-1", userID: "u-1", walletName: "Test", initialAmount: -1, currency: "EUR", category: "Saving",
			wantErr: wallet.ErrInvalidInitialAmount,
		},
		{
			name:     "initial amount too high",
			walletID: "w-1", userID: "u-1", walletName: "Test", initialAmount: 10_000_001, currency: "EUR", category: "Saving",
			wantErr: wallet.ErrInvalidInitialAmount,
		},
		{
			name:     "invalid currency",
			walletID: "w-1", userID: "u-1", walletName: "Test", initialAmount: 0, currency: "GBP", category: "Saving",
			wantErr: wallet.ErrInvalidCurrency,
		},
		{
			name:     "invalid category",
			walletID: "w-1", userID: "u-1", walletName: "Test", initialAmount: 0, currency: "EUR", category: "Crypto",
			wantErr: wallet.ErrInvalidCategory,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := wallet.Create(tt.walletID, tt.userID, tt.walletName, tt.description, tt.initialAmount, tt.currency, tt.category, now)
			if err != tt.wantErr {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeposit(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	depositTime := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)

	createWallet := func() *wallet.Aggregate {
		agg, err := wallet.Create("wallet-1", "user-1", "My Savings", "", 10000, "EUR", "Saving", now)
		if err != nil {
			t.Fatalf("failed to create wallet: %v", err)
		}
		return agg
	}

	t.Run("deposits money successfully", func(t *testing.T) {
		agg := createWallet()

		err := agg.Deposit("user-1", 5000, "EUR", "Birthday gift", depositTime)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Balance() != 15000 {
			t.Errorf("Balance = %d, want %d", agg.Balance(), 15000)
		}
		if len(agg.Changes()) != 2 {
			t.Fatalf("Changes = %d, want 2", len(agg.Changes()))
		}
		if agg.Changes()[1].EventType() != "MoneyDeposited" {
			t.Errorf("EventType = %q, want %q", agg.Changes()[1].EventType(), "MoneyDeposited")
		}
	})

	t.Run("deposits without reason", func(t *testing.T) {
		agg := createWallet()

		err := agg.Deposit("user-1", 1000, "EUR", "", depositTime)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Balance() != 11000 {
			t.Errorf("Balance = %d, want %d", agg.Balance(), 11000)
		}
	})

	tests := []struct {
		name     string
		userID   string
		amount   int
		currency string
		reason   string
		wantErr  error
	}{
		{
			name:     "zero amount",
			userID:   "user-1",
			amount:   0,
			currency: "EUR",
			wantErr:  wallet.ErrInvalidAmount,
		},
		{
			name:     "negative amount",
			userID:   "user-1",
			amount:   -100,
			currency: "EUR",
			wantErr:  wallet.ErrInvalidAmount,
		},
		{
			name:     "currency mismatch",
			userID:   "user-1",
			amount:   5000,
			currency: "USD",
			wantErr:  wallet.ErrCurrencyMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := createWallet()
			err := agg.Deposit(tt.userID, tt.amount, tt.currency, tt.reason, depositTime)
			if err != tt.wantErr {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestWithdraw(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	withdrawTime := time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC)

	createWallet := func() *wallet.Aggregate {
		agg, err := wallet.Create("wallet-1", "user-1", "My Savings", "", 10000, "EUR", "Saving", now)
		if err != nil {
			t.Fatalf("failed to create wallet: %v", err)
		}
		return agg
	}

	t.Run("withdraws money successfully", func(t *testing.T) {
		agg := createWallet()

		err := agg.Withdraw("user-1", 3000, "EUR", "Lunch", "Food", withdrawTime)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Balance() != 7000 {
			t.Errorf("Balance = %d, want %d", agg.Balance(), 7000)
		}
		if len(agg.Changes()) != 2 {
			t.Fatalf("Changes = %d, want 2", len(agg.Changes()))
		}
		if agg.Changes()[1].EventType() != "MoneyWithdrawn" {
			t.Errorf("EventType = %q, want %q", agg.Changes()[1].EventType(), "MoneyWithdrawn")
		}
	})

	t.Run("withdraws entire balance", func(t *testing.T) {
		agg := createWallet()

		err := agg.Withdraw("user-1", 10000, "EUR", "", "Other", withdrawTime)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.Balance() != 0 {
			t.Errorf("Balance = %d, want %d", agg.Balance(), 0)
		}
	})

	tests := []struct {
		name     string
		userID   string
		amount   int
		currency string
		reason   string
		category string
		wantErr  error
	}{
		{
			name: "zero amount", userID: "user-1", amount: 0, currency: "EUR", category: "Food",
			wantErr: wallet.ErrInvalidAmount,
		},
		{
			name: "negative amount", userID: "user-1", amount: -100, currency: "EUR", category: "Food",
			wantErr: wallet.ErrInvalidAmount,
		},
		{
			name: "currency mismatch", userID: "user-1", amount: 5000, currency: "USD", category: "Food",
			wantErr: wallet.ErrCurrencyMismatch,
		},
		{
			name: "invalid category", userID: "user-1", amount: 5000, currency: "EUR", category: "InvalidCat",
			wantErr: wallet.ErrInvalidWithdrawCategory,
		},
		{
			name: "insufficient funds", userID: "user-1", amount: 10001, currency: "EUR", category: "Food",
			wantErr: wallet.ErrInsufficientFunds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := createWallet()
			err := agg.Withdraw(tt.userID, tt.amount, tt.currency, tt.reason, tt.category, withdrawTime)
			if err != tt.wantErr {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAggregate_Apply_WalletAdded(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	agg := wallet.NewAggregate()
	agg.Apply(wallet.WalletAdded{
		WalletID:      "wallet-1",
		UserID:        "user-1",
		Name:          "My Savings",
		Description:   "Main savings",
		InitialAmount: 50000,
		Currency:      "EUR",
		Category:      "Saving",
		CreatedAt:     now,
	})

	if agg.ID() != "wallet-1" {
		t.Errorf("ID = %q, want %q", agg.ID(), "wallet-1")
	}
	if agg.Balance() != 50000 {
		t.Errorf("Balance = %d, want %d", agg.Balance(), 50000)
	}
	if agg.Version() != 1 {
		t.Errorf("Version = %d, want %d", agg.Version(), 1)
	}
	if len(agg.Changes()) != 0 {
		t.Errorf("Changes = %d, want 0 (Apply should not track changes)", len(agg.Changes()))
	}
}
