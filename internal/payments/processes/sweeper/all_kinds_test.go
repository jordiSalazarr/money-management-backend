package sweeper_test

import (
	"context"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/recorddebt"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/settledebt"
	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/processes/debtsettlement"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence"
	occurrencedomain "github.com/jordisalazar/money-management/internal/payments/processes/occurrence/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/projections"
	"github.com/jordisalazar/money-management/internal/payments/processes/sweeper"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	walletdomain "github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// These cover the two process kinds the sweeper was wired for but could never
// actually reach: processes_view only ever held transfers, so a stalled
// occurrence or debt settlement was invisible to it and would have sat
// un-settled forever. The transfer-only sweeper test passed throughout.

type allKinds struct {
	store   *eventstore.MemoryStore
	codec   *eventstore.Codec
	views   *projections.MemoryProcessStore
	sweeper *sweeper.Sweeper
}

func newAllKinds(t *testing.T, opts sweeper.Options) *allKinds {
	t.Helper()
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	views := projections.NewMemoryProcessStore()

	clock := func() time.Time { return muchLater }
	return &allKinds{
		store: store, codec: codec, views: views,
		sweeper: sweeper.New(views, map[string]sweeper.Driver{
			projections.KindOccurrence:     occurrence.NewReactor(store, codec, clock),
			projections.KindDebtSettlement: debtsettlement.NewReactor(store, codec, clock),
		}, opts, nil),
	}
}

func (f *allKinds) project(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	head, err := f.store.Head(ctx)
	if err != nil {
		t.Fatalf("reading the log head: %v", err)
	}
	if head == eventstore.Start {
		return
	}
	p := projections.NewProcessesProjector(f.views)
	_ = f.store.SubscribeAll(ctx, eventstore.Start, func(r eventstore.Recorded) error {
		decoded, err := f.codec.Decode(r)
		if err != nil {
			return err
		}
		p.Handle(decoded, r.Meta())
		if r.Position == head {
			return errCaughtUp
		}
		return nil
	})
}

func (f *allKinds) wallet(t *testing.T, balance int) string {
	t.Helper()
	id, err := addwallet.NewCommandHandler(f.store, f.codec).Handle(context.Background(), addwallet.Command{
		UserID: "user-1", Name: "w", InitialAmount: balance, Currency: "EUR", Category: "Cash",
	})
	if err != nil {
		t.Fatalf("creating wallet: %v", err)
	}
	return id
}

func (f *allKinds) balance(t *testing.T, walletID string) int {
	t.Helper()
	agg := walletdomain.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), f.store, f.codec, streams.Wallet(walletID), agg.Apply); err != nil {
		t.Fatalf("loading wallet: %v", err)
	}
	return agg.Balance()
}

// sweep runs a pass well after the processes were opened.
//
// It uses the wall clock rather than the fixed test time because settledebt's
// command handler stamps time.Now() internally and takes no clock — so a
// fixed sweep time would land on the threshold boundary and flake. Sweeping a
// day out makes "this has been sitting long enough" unambiguous.
func (f *allKinds) sweep(t *testing.T) sweeper.Report {
	t.Helper()
	f.project(t)
	report, err := f.sweeper.Sweep(context.Background(), time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	f.project(t)
	return report
}

func TestAStalledOccurrenceIsRecovered(t *testing.T) {
	f := newAllKinds(t, sweeper.Options{})
	w := f.wallet(t, 50000)

	// The cron opened the occurrence; the reactor never ran.
	stream := streams.Occurrence("rm-1", requestedAt)
	agg, err := occurrencedomain.Due(stream.ID, "rm-1", w, "user-1", "withdrawal", 9900, "EUR", "gym", requestedAt, requestedAt)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if err := eventstore.AppendChanges(context.Background(), f.store, f.codec,
		stream, eventstore.NoStream, agg.Changes(), "corr-1", requestedAt); err != nil {
		t.Fatalf("appending: %v", err)
	}

	report := f.sweep(t)
	if report.Examined != 1 {
		t.Fatalf("Examined = %d, want 1 — a stalled occurrence must be visible to the sweeper", report.Examined)
	}
	if got := f.balance(t, w); got != 40100 {
		t.Errorf("balance = %d, want 40100 — the sweeper should have driven the movement", got)
	}
}

func TestAStalledDebtSettlementIsRecovered(t *testing.T) {
	f := newAllKinds(t, sweeper.Options{})
	w := f.wallet(t, 50000)

	debtID, err := recorddebt.NewCommandHandler(f.store, f.codec).Handle(context.Background(), recorddebt.Command{
		OwnerID: "user-1", WalletID: w, Amount: 5000, Currency: "EUR", Direction: "OUT", Reason: "dinner",
	})
	if err != nil {
		t.Fatalf("recording debt: %v", err)
	}
	// Requested, but the reactor never ran.
	if err := settledebt.NewCommandHandler(f.store, f.codec).Handle(context.Background(), settledebt.Command{
		DebtID: debtID, SettledBy: "user-1",
	}); err != nil {
		t.Fatalf("requesting settlement: %v", err)
	}

	report := f.sweep(t)
	if report.Examined != 1 {
		t.Fatalf("Examined = %d, want 1 — a stalled settlement must be visible to the sweeper", report.Examined)
	}
	if got := f.balance(t, w); got != 45000 {
		t.Errorf("balance = %d, want 45000 — the sweeper should have driven the payment", got)
	}
}

// TestASettledOccurrenceIsNotSweptForever guards the other half: terminal
// statuses for every kind must be recognised, or the sweeper re-drives
// processes that already finished.
func TestASettledOccurrenceIsNotSweptForever(t *testing.T) {
	f := newAllKinds(t, sweeper.Options{})
	w := f.wallet(t, 50000)

	stream := streams.Occurrence("rm-1", requestedAt)
	agg, _ := occurrencedomain.Due(stream.ID, "rm-1", w, "user-1", "withdrawal", 9900, "EUR", "gym", requestedAt, requestedAt)
	_ = eventstore.AppendChanges(context.Background(), f.store, f.codec,
		stream, eventstore.NoStream, agg.Changes(), "corr-1", requestedAt)

	f.sweep(t) // settles it
	after := f.balance(t, w)

	report := f.sweep(t)
	if report.Examined != 0 {
		t.Errorf("Examined = %d, want 0 — an executed occurrence is terminal", report.Examined)
	}
	if got := f.balance(t, w); got != after {
		t.Errorf("balance moved from %d to %d — a settled occurrence must not be re-driven", after, got)
	}
}

// TestADebtSettlementAccruesAttempts guards the no-op RecordAttempt: without a
// real counter the sweeper's budget never advances and it re-drives forever.
func TestADebtSettlementAccruesAttempts(t *testing.T) {
	f := newAllKinds(t, sweeper.Options{MaxAttempts: 2})
	w := f.wallet(t, 100) // too little, so the settlement keeps failing to progress

	debtID, _ := recorddebt.NewCommandHandler(f.store, f.codec).Handle(context.Background(), recorddebt.Command{
		OwnerID: "user-1", WalletID: w, Amount: 5000, Currency: "EUR", Direction: "OUT", Reason: "dinner",
	})
	_ = settledebt.NewCommandHandler(f.store, f.codec).Handle(context.Background(), settledebt.Command{
		DebtID: debtID, SettledBy: "user-1",
	})

	// The first sweep drives it; the wallet refuses, so it settles as failed.
	f.sweep(t)

	agg := debt.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), f.store, f.codec, streams.Debt(debtID), agg.Apply); err != nil {
		t.Fatalf("loading debt: %v", err)
	}
	// Either it settled as failed, or it recorded an attempt. What must NOT
	// happen is it sitting at zero attempts and still in flight forever.
	if agg.Status() == debt.DebtStatusSettling && agg.Attempts() == 0 {
		t.Fatal("settlement is still in flight with no attempts recorded — the sweeper would re-drive it forever")
	}
}
