package wallet_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
)

func TestCreate(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	t.Run("creates wallet with valid inputs", func(t *testing.T) {
		agg, err := wallet.Create("wallet-1", "user-1", "My Savings", "Main savings", 50000, "EUR", "Savings", now)
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
		if agg.Category() != "Savings" {
			t.Errorf("Category = %q, want %q", agg.Category(), "Savings")
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

	t.Run("creates wallet with all valid categories", func(t *testing.T) {
		cats := []string{"Cash", "Checking", "Savings", "Credit Card", "Investment", "Digital Wallet", "Loan", "Other"}
		for _, cat := range cats {
			_, err := wallet.Create("w-"+cat, "user-1", "Test", "", 0, "EUR", cat, now)
			if err != nil {
				t.Errorf("category %q: unexpected error: %v", cat, err)
			}
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
			walletID: "w-1", userID: "", walletName: "Test", initialAmount: 0, currency: "EUR", category: "Savings",
			wantErr: wallet.ErrMissingUserID,
		},
		{
			name:     "missing name",
			walletID: "w-1", userID: "u-1", walletName: "", initialAmount: 0, currency: "EUR", category: "Savings",
			wantErr: wallet.ErrMissingName,
		},
		{
			name:     "negative initial amount",
			walletID: "w-1", userID: "u-1", walletName: "Test", initialAmount: -1, currency: "EUR", category: "Savings",
			wantErr: wallet.ErrInvalidInitialAmount,
		},
		{
			name:     "initial amount too high",
			walletID: "w-1", userID: "u-1", walletName: "Test", initialAmount: 10_000_001, currency: "EUR", category: "Savings",
			wantErr: wallet.ErrInvalidInitialAmount,
		},
		{
			name:     "invalid currency",
			walletID: "w-1", userID: "u-1", walletName: "Test", initialAmount: 0, currency: "GBP", category: "Savings",
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
		agg, err := wallet.Create("wallet-1", "user-1", "My Savings", "", 10000, "EUR", "Savings", now)
		if err != nil {
			t.Fatalf("failed to create wallet: %v", err)
		}
		return agg
	}

	t.Run("deposits money successfully", func(t *testing.T) {
		agg := createWallet()

		err := agg.Deposit("user-1", 5000, "EUR", "Birthday gift", "Gift", "", depositTime)
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

		err := agg.Deposit("user-1", 1000, "EUR", "", "Salary", "", depositTime)
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
		category string
		wantErr  error
	}{
		{
			name:     "zero amount",
			userID:   "user-1",
			amount:   0,
			currency: "EUR",
			category: "Salary",
			wantErr:  wallet.ErrInvalidAmount,
		},
		{
			name:     "negative amount",
			userID:   "user-1",
			amount:   -100,
			currency: "EUR",
			category: "Salary",
			wantErr:  wallet.ErrInvalidAmount,
		},
		{
			name:     "currency mismatch",
			userID:   "user-1",
			amount:   5000,
			currency: "USD",
			category: "Salary",
			wantErr:  wallet.ErrCurrencyMismatch,
		},
		{
			name:     "invalid deposit category",
			userID:   "user-1",
			amount:   5000,
			currency: "EUR",
			category: "InvalidCat",
			wantErr:  wallet.ErrInvalidDepositCategory,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := createWallet()
			err := agg.Deposit(tt.userID, tt.amount, tt.currency, tt.reason, tt.category, "", depositTime)
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
		agg, err := wallet.Create("wallet-1", "user-1", "My Savings", "", 10000, "EUR", "Savings", now)
		if err != nil {
			t.Fatalf("failed to create wallet: %v", err)
		}
		return agg
	}

	t.Run("withdraws money successfully", func(t *testing.T) {
		agg := createWallet()

		err := agg.Withdraw("user-1", 3000, "EUR", "Lunch", "Food & Groceries", "", withdrawTime)
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

		err := agg.Withdraw("user-1", 10000, "EUR", "", "Other", "", withdrawTime)
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
			name: "zero amount", userID: "user-1", amount: 0, currency: "EUR", category: "Food & Groceries",
			wantErr: wallet.ErrInvalidAmount,
		},
		{
			name: "negative amount", userID: "user-1", amount: -100, currency: "EUR", category: "Food & Groceries",
			wantErr: wallet.ErrInvalidAmount,
		},
		{
			name: "currency mismatch", userID: "user-1", amount: 5000, currency: "USD", category: "Food & Groceries",
			wantErr: wallet.ErrCurrencyMismatch,
		},
		{
			name: "invalid category", userID: "user-1", amount: 5000, currency: "EUR", category: "InvalidCat",
			wantErr: wallet.ErrInvalidWithdrawCategory,
		},
		{
			name: "insufficient funds", userID: "user-1", amount: 10001, currency: "EUR", category: "Food & Groceries",
			wantErr: wallet.ErrInsufficientFunds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := createWallet()
			err := agg.Withdraw(tt.userID, tt.amount, tt.currency, tt.reason, tt.category, "", withdrawTime)
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
		Category:      "Savings",
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

func TestDisable(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	disableTime := time.Date(2026, 4, 26, 9, 0, 0, 0, time.UTC)

	createWallet := func() *wallet.Aggregate {
		agg, err := wallet.Create("wallet-1", "user-1", "My Savings", "", 10000, "EUR", "Savings", now)
		if err != nil {
			t.Fatalf("failed to create wallet: %v", err)
		}
		return agg
	}

	t.Run("disables wallet successfully", func(t *testing.T) {
		agg := createWallet()

		err := agg.Disable("user-1", "no longer needed", disableTime)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !agg.Disabled() {
			t.Error("Disabled = false, want true")
		}
		if len(agg.Changes()) != 2 {
			t.Fatalf("Changes = %d, want 2", len(agg.Changes()))
		}
		if agg.Changes()[1].EventType() != "WalletDisabled" {
			t.Errorf("EventType = %q, want %q", agg.Changes()[1].EventType(), "WalletDisabled")
		}
	})

	t.Run("disables wallet without reason", func(t *testing.T) {
		agg := createWallet()

		err := agg.Disable("user-1", "", disableTime)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !agg.Disabled() {
			t.Error("Disabled = false, want true")
		}
	})

	t.Run("already disabled", func(t *testing.T) {
		agg := createWallet()

		if err := agg.Disable("user-1", "first", disableTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err := agg.Disable("user-1", "second", disableTime)
		if err != wallet.ErrWalletAlreadyDisabled {
			t.Errorf("err = %v, want %v", err, wallet.ErrWalletAlreadyDisabled)
		}
	})

	t.Run("deposit blocked on disabled wallet", func(t *testing.T) {
		agg := createWallet()

		if err := agg.Disable("user-1", "done", disableTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err := agg.Deposit("user-1", 5000, "EUR", "gift", "Gift", "", disableTime)
		if err != wallet.ErrWalletDisabled {
			t.Errorf("err = %v, want %v", err, wallet.ErrWalletDisabled)
		}
	})

	t.Run("withdraw blocked on disabled wallet", func(t *testing.T) {
		agg := createWallet()

		if err := agg.Disable("user-1", "done", disableTime); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err := agg.Withdraw("user-1", 3000, "EUR", "lunch", "Food & Groceries", "", disableTime)
		if err != wallet.ErrWalletDisabled {
			t.Errorf("err = %v, want %v", err, wallet.ErrWalletDisabled)
		}
	})
}

func TestAggregate_Apply_WalletDisabled(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	disableTime := time.Date(2026, 4, 26, 9, 0, 0, 0, time.UTC)

	agg := wallet.NewAggregate()
	agg.Apply(wallet.WalletAdded{
		WalletID:      "wallet-1",
		UserID:        "user-1",
		Name:          "My Savings",
		Description:   "",
		InitialAmount: 10000,
		Currency:      "EUR",
		Category:      "Savings",
		CreatedAt:     now,
	})
	agg.Apply(wallet.WalletDisabled{
		WalletID:   "wallet-1",
		UserID:     "user-1",
		DisabledAt: disableTime,
		Reason:     "cleanup",
	})

	if !agg.Disabled() {
		t.Error("Disabled = false, want true")
	}
	if agg.Version() != 2 {
		t.Errorf("Version = %d, want %d", agg.Version(), 2)
	}
	if len(agg.Changes()) != 0 {
		t.Errorf("Changes = %d, want 0 (Apply should not track changes)", len(agg.Changes()))
	}
}

func TestIdempotencyKey(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	createWallet := func() *wallet.Aggregate {
		agg, err := wallet.Create("wallet-1", "user-1", "Test", "", 0, "EUR", "Cash", now)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return agg
	}

	t.Run("deposit with empty key always succeeds", func(t *testing.T) {
		agg := createWallet()
		if err := agg.Deposit("user-1", 100, "EUR", "", "Salary", "", now); err != nil {
			t.Fatalf("first deposit: %v", err)
		}
		if err := agg.Deposit("user-1", 100, "EUR", "", "Salary", "", now); err != nil {
			t.Fatalf("second deposit with empty key: %v", err)
		}
		if agg.Balance() != 200 {
			t.Errorf("balance = %d, want 200 (two empty-key deposits should both apply)", agg.Balance())
		}
	})

	t.Run("deposit with same non-empty key short-circuits", func(t *testing.T) {
		agg := createWallet()
		if err := agg.Deposit("user-1", 100, "EUR", "", "Salary", "evt-1", now); err != nil {
			t.Fatalf("first deposit: %v", err)
		}
		err := agg.Deposit("user-1", 100, "EUR", "", "Salary", "evt-1", now)
		if !errors.Is(err, wallet.ErrAlreadyProcessed) {
			t.Fatalf("got %v, want ErrAlreadyProcessed", err)
		}
		if agg.Balance() != 100 {
			t.Errorf("balance = %d, want 100 (second call should be a no-op)", agg.Balance())
		}
	})

	t.Run("withdraw with same non-empty key short-circuits", func(t *testing.T) {
		agg := createWallet()
		if err := agg.Deposit("user-1", 500, "EUR", "", "Salary", "", now); err != nil {
			t.Fatalf("seed deposit: %v", err)
		}
		if err := agg.Withdraw("user-1", 200, "EUR", "", "Food & Groceries", "evt-w-1", now); err != nil {
			t.Fatalf("first withdraw: %v", err)
		}
		err := agg.Withdraw("user-1", 200, "EUR", "", "Food & Groceries", "evt-w-1", now)
		if !errors.Is(err, wallet.ErrAlreadyProcessed) {
			t.Fatalf("got %v, want ErrAlreadyProcessed", err)
		}
		if agg.Balance() != 300 {
			t.Errorf("balance = %d, want 300", agg.Balance())
		}
	})

	t.Run("idempotency keys rebuild on hydration", func(t *testing.T) {
		agg := wallet.NewAggregate()
		agg.Apply(wallet.WalletAdded{
			WalletID: "wallet-1", UserID: "user-1", Name: "Test",
			InitialAmount: 0, Currency: "EUR", Category: "Cash", CreatedAt: now,
		})
		agg.Apply(wallet.MoneyDeposited{
			WalletID: "wallet-1", UserID: "user-1", Amount: 100,
			Currency: "EUR", Category: "Salary", DepositedAt: now,
			IdempotencyKey: "evt-1",
		})

		err := agg.Deposit("user-1", 100, "EUR", "", "Salary", "evt-1", now)
		if !errors.Is(err, wallet.ErrAlreadyProcessed) {
			t.Fatalf("got %v, want ErrAlreadyProcessed after hydration", err)
		}
	})
}
