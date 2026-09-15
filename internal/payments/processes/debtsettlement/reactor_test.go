package debtsettlement_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/recorddebt"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/settledebt"
	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/processes/debtsettlement"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	walletdomain "github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

var at = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

var errCaughtUp = errors.New("caught up")

type fixture struct {
	store   *eventstore.MemoryStore
	codec   *eventstore.Codec
	reactor *debtsettlement.Reactor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	return &fixture{store, codec, debtsettlement.NewReactor(store, codec, func() time.Time { return at })}
}

func (f *fixture) wallet(t *testing.T, balance int) string {
	t.Helper()
	id, err := addwallet.NewCommandHandler(f.store, f.codec).Handle(context.Background(), addwallet.Command{
		UserID: "user-1", Name: "w", InitialAmount: balance, Currency: "EUR", Category: "Cash",
	})
	if err != nil {
		t.Fatalf("creating wallet: %v", err)
	}
	return id
}

func (f *fixture) debt(t *testing.T, walletID, direction string, amount int) string {
	t.Helper()
	id, err := recorddebt.NewCommandHandler(f.store, f.codec).Handle(context.Background(), recorddebt.Command{
		OwnerID: "user-1", WalletID: walletID, Amount: amount,
		Currency: "EUR", Direction: direction, Reason: "dinner split",
	})
	if err != nil {
		t.Fatalf("recording debt: %v", err)
	}
	return id
}

func (f *fixture) requestSettlement(t *testing.T, debtID string) {
	t.Helper()
	err := settledebt.NewCommandHandler(f.store, f.codec).Handle(context.Background(), settledebt.Command{
		DebtID: debtID, SettledBy: "user-1",
	})
	if err != nil {
		t.Fatalf("requesting settlement: %v", err)
	}
}

func (f *fixture) debtState(t *testing.T, debtID string) *debt.Aggregate {
	t.Helper()
	agg := debt.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), f.store, f.codec, streams.Debt(debtID), agg.Apply); err != nil {
		t.Fatalf("loading debt: %v", err)
	}
	return agg
}

func (f *fixture) balance(t *testing.T, walletID string) int {
	t.Helper()
	agg := walletdomain.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), f.store, f.codec, streams.Wallet(walletID), agg.Apply); err != nil {
		t.Fatalf("loading wallet: %v", err)
	}
	return agg.Balance()
}

func (f *fixture) drive(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	head := f.store.Head()
	if head == eventstore.Start {
		return
	}
	var batch []eventstore.Recorded
	err := f.store.SubscribeAll(ctx, eventstore.Start, func(r eventstore.Recorded) error {
		batch = append(batch, r)
		if r.Position == head {
			return errCaughtUp
		}
		return nil
	})
	if err != nil && !errors.Is(err, errCaughtUp) {
		t.Fatalf("reading the log: %v", err)
	}
	for _, r := range batch {
		decoded, err := f.codec.Decode(r)
		if err != nil {
			t.Fatalf("decoding %s: %v", r.EventType, err)
		}
		if err := f.reactor.Handle(context.Background(), decoded, r.Meta()); err != nil {
			t.Fatalf("reactor on %s: %v", r.EventType, err)
		}
	}
}

func TestSettlingADebtTheOwnerOwes(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 10000)
	d := f.debt(t, w, "OUT", 5000)
	f.requestSettlement(t, d)

	f.drive(t)

	if got := f.debtState(t, d).Status(); got != debt.DebtStatusSettled {
		t.Fatalf("Status = %q, want settled", got)
	}
	if got := f.balance(t, w); got != 5000 {
		t.Errorf("balance = %d, want 5000 — the money must have left", got)
	}
}

func TestSettlingADebtOwedToTheOwner(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 0)
	d := f.debt(t, w, "IN", 5000)
	f.requestSettlement(t, d)

	f.drive(t)

	if got := f.debtState(t, d).Status(); got != debt.DebtStatusSettled {
		t.Fatalf("Status = %q, want settled", got)
	}
	if got := f.balance(t, w); got != 5000 {
		t.Errorf("balance = %d, want 5000", got)
	}
}

// TestInsufficientFundsDoesNotSettleTheDebt is the live bug. Before the
// process existed, DebtSettled was written before the money moved, the
// withdrawal was refused, the record was dropped with a log.Warn, and the debt
// read settled with nothing paid.
func TestInsufficientFundsDoesNotSettleTheDebt(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 100) // nowhere near enough
	d := f.debt(t, w, "OUT", 5000)
	f.requestSettlement(t, d)

	f.drive(t)

	agg := f.debtState(t, d)
	if agg.Status() == debt.DebtStatusSettled {
		t.Fatal("THE DEBT MUST NOT READ SETTLED — no money moved")
	}
	// It goes back to standing, so the owner can settle it once they have the
	// funds. A one-way trip to "failed" would strand the debt forever.
	if agg.Status() != debt.DebtStatusCreated {
		t.Errorf("Status = %q, want created — the debt still stands", agg.Status())
	}
	if got := f.balance(t, w); got != 100 {
		t.Errorf("balance = %d, want 100 — untouched", got)
	}
}

func TestAFailedSettlementCanBeRetriedLater(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 100)
	d := f.debt(t, w, "OUT", 5000)
	f.requestSettlement(t, d)
	f.drive(t)

	// The owner tops the wallet up and tries again.
	if _, err := addwallet.NewCommandHandler(f.store, f.codec).Handle(context.Background(), addwallet.Command{
		UserID: "user-1", Name: "other", InitialAmount: 0, Currency: "EUR", Category: "Cash",
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	depositTopUp(t, f, w, 10000)

	f.requestSettlement(t, d)
	f.drive(t)

	if got := f.debtState(t, d).Status(); got != debt.DebtStatusSettled {
		t.Fatalf("Status = %q, want settled on the second attempt", got)
	}
}

func TestRedeliveryDoesNotPayTwice(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 10000)
	d := f.debt(t, w, "OUT", 5000)
	f.requestSettlement(t, d)

	f.drive(t)
	f.drive(t)
	f.drive(t)

	if got := f.balance(t, w); got != 5000 {
		t.Errorf("balance = %d, want 5000 — redelivery must not pay twice", got)
	}
}
