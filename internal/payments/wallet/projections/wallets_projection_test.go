package projections_test

import (
	"context"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

var meta = eventstore.EventMetadata{EventID: "evt-1"}

func projectorWithWallet(t *testing.T, opening int) (*projections.WalletsProjector, *projections.MemoryWalletStore) {
	t.Helper()
	store := projections.NewMemoryWalletStore()
	p := projections.NewWalletsProjector(store)
	if err := p.Handle(wallet.WalletAdded{
		WalletID: "w1", UserID: "u1", Name: "Everyday",
		InitialAmount: opening, Currency: "EUR", Category: "Cash",
		CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	}, meta); err != nil {
		t.Fatalf("seeding wallet: %v", err)
	}
	return p, store
}

func balance(t *testing.T, store *projections.MemoryWalletStore) int {
	t.Helper()
	ws, err := store.ListByUserID(context.Background(), "u1")
	if err != nil {
		t.Fatalf("listing wallets: %v", err)
	}
	if len(ws) != 1 {
		t.Fatalf("want 1 wallet, got %d", len(ws))
	}
	return ws[0].CurrentBalance
}

// TestARefundRestoresTheProjectedBalance is the drift guard.
//
// MoneyRefunded was handled by the movements ledger but not here, so after a
// compensated transfer the wallet read short by the refunded amount — forever,
// and the projected balance is the number a person actually sees.
func TestARefundRestoresTheProjectedBalance(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	p, store := projectorWithWallet(t, 10000)

	if err := p.Handle(wallet.MoneyWithdrawn{
		WalletID: "w1", Amount: 5000, Currency: "EUR", WithdrawnAt: now,
		IdempotencyKey: "tx-1:debit",
	}, meta); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if got := balance(t, store); got != 5000 {
		t.Fatalf("after the debit balance = %d, want 5000", got)
	}

	if err := p.Handle(wallet.MoneyRefunded{
		WalletID: "w1", Amount: 5000, Currency: "EUR", RefundedAt: now,
		IdempotencyKey: "tx-1:refund",
	}, meta); err != nil {
		t.Fatalf("refund: %v", err)
	}

	if got := balance(t, store); got != 10000 {
		t.Fatalf("balance = %d, want 10000 — the projection must agree with the aggregate", got)
	}
}

func TestEveryBalanceChangingEventIsHandled(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		event any
		want  int
	}{
		{"deposit", wallet.MoneyDeposited{WalletID: "w1", Amount: 250, Currency: "EUR", DepositedAt: now}, 10250},
		{"withdrawal", wallet.MoneyWithdrawn{WalletID: "w1", Amount: 250, Currency: "EUR", WithdrawnAt: now}, 9750},
		{"refund", wallet.MoneyRefunded{WalletID: "w1", Amount: 250, Currency: "EUR", RefundedAt: now}, 10250},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, store := projectorWithWallet(t, 10000)
			if err := p.Handle(c.event, meta); err != nil {
				t.Fatalf("handle: %v", err)
			}
			if got := balance(t, store); got != c.want {
				t.Errorf("balance = %d, want %d", got, c.want)
			}
		})
	}
}

// An event for a wallet this projection never saw created means the read model
// is already inconsistent. Advancing the checkpoint past it would make that
// permanent, so it has to be an error rather than a silent skip.
func TestAnEventForAnUnknownWalletIsAnError(t *testing.T) {
	store := projections.NewMemoryWalletStore()
	p := projections.NewWalletsProjector(store)

	err := p.Handle(wallet.MoneyDeposited{
		WalletID: "never-seen", Amount: 100, Currency: "EUR",
		DepositedAt: time.Now().UTC(),
	}, meta)
	if err == nil {
		t.Fatal("want an error for an event whose wallet was never projected")
	}
}

func TestDisablingIsProjected(t *testing.T) {
	p, store := projectorWithWallet(t, 10000)
	if err := p.Handle(wallet.WalletDisabled{
		WalletID: "w1", UserID: "u1", Reason: "closing", DisabledAt: time.Now().UTC(),
	}, meta); err != nil {
		t.Fatalf("disable: %v", err)
	}

	ws, _ := store.ListByUserID(context.Background(), "u1")
	if !ws[0].Disabled {
		t.Error("wallet should read disabled")
	}
}
