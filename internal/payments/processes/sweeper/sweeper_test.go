package sweeper_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/processes/projections"
	"github.com/jordisalazar/money-management/internal/payments/processes/sweeper"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer"
	transferdomain "github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	walletdomain "github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

var (
	requestedAt = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	// Well past the 10-minute threshold.
	muchLater   = requestedAt.Add(time.Hour)
	errCaughtUp = errors.New("caught up")
)

type fixture struct {
	store   *eventstore.MemoryStore
	codec   *eventstore.Codec
	views   *projections.MemoryProcessStore
	reactor *transfer.Reactor
	sweeper *sweeper.Sweeper
}

func newFixture(t *testing.T, opts sweeper.Options) *fixture {
	t.Helper()
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	views := projections.NewMemoryProcessStore()
	reactor := transfer.NewReactor(store, codec, func() time.Time { return muchLater })

	return &fixture{
		store:   store,
		codec:   codec,
		views:   views,
		reactor: reactor,
		sweeper: sweeper.New(views, map[string]sweeper.Driver{
			projections.KindTransfer: reactor,
		}, opts, nil),
	}
}

func (f *fixture) wallet(t *testing.T, currency string, balance int) string {
	t.Helper()
	id, err := addwallet.NewCommandHandler(f.store, f.codec).Handle(context.Background(), addwallet.Command{
		UserID: "user-1", Name: "w", InitialAmount: balance, Currency: currency, Category: "Cash",
	})
	if err != nil {
		t.Fatalf("creating wallet: %v", err)
	}
	return id
}

func (f *fixture) requestTransfer(t *testing.T, id, from, to string, amount int, currency string) {
	t.Helper()
	agg, err := transferdomain.Request(id, "user-1", from, to, amount, currency, "rent", requestedAt)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := eventstore.AppendChanges(context.Background(), f.store, f.codec,
		streams.Transfer(id), eventstore.NoStream, agg.Changes(), "corr-1", requestedAt); err != nil {
		t.Fatalf("appending transfer: %v", err)
	}
}

// project brings processes_view up to date with the log, the way the real
// projector would. The sweeper reads the view, not the streams.
func (f *fixture) project(t *testing.T) {
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
	projector := projections.NewProcessesProjector(f.views)
	err = f.store.SubscribeAll(ctx, eventstore.Start, func(r eventstore.Recorded) error {
		decoded, err := f.codec.Decode(r)
		if err != nil {
			return err
		}
		projector.Handle(decoded, r.Meta())
		if r.Position == head {
			return errCaughtUp
		}
		return nil
	})
	if err != nil && !errors.Is(err, errCaughtUp) {
		t.Fatalf("projecting: %v", err)
	}
}

func (f *fixture) transferState(t *testing.T, id string) *transferdomain.Aggregate {
	t.Helper()
	agg := transferdomain.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), f.store, f.codec, streams.Transfer(id), agg.Apply); err != nil {
		t.Fatalf("loading transfer: %v", err)
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

func (f *fixture) sweep(t *testing.T, now time.Time) sweeper.Report {
	t.Helper()
	f.project(t)
	report, err := f.sweeper.Sweep(context.Background(), now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	f.project(t)
	return report
}

// TestAStalledTransferIsRecovered is the reactor-was-down case: a transfer was
// requested, nothing ever picked it up, and before the sweeper existed it
// would have stayed "requested" forever.
func TestAStalledTransferIsRecovered(t *testing.T) {
	f := newFixture(t, sweeper.Options{})
	from := f.wallet(t, "EUR", 10000)
	to := f.wallet(t, "EUR", 0)
	f.requestTransfer(t, "tx-1", from, to, 5000, "EUR")

	// The reactor never ran. The sweeper re-drives it through both legs.
	for i := 0; i < 3 && !f.transferState(t, "tx-1").IsTerminal(); i++ {
		f.sweep(t, muchLater)
	}

	if got := f.transferState(t, "tx-1").Status(); got != transferdomain.StatusCompleted {
		t.Fatalf("Status = %q, want completed — the sweeper should have recovered it", got)
	}
	if got := f.balance(t, from); got != 5000 {
		t.Errorf("source balance = %d, want 5000", got)
	}
	if got := f.balance(t, to); got != 5000 {
		t.Errorf("destination balance = %d, want 5000", got)
	}
}

func TestATransferUnderTheThresholdIsLeftAlone(t *testing.T) {
	f := newFixture(t, sweeper.Options{Threshold: time.Hour})
	from := f.wallet(t, "EUR", 10000)
	to := f.wallet(t, "EUR", 0)
	f.requestTransfer(t, "tx-1", from, to, 5000, "EUR")

	// One minute old, threshold an hour: still in flight, not stalled.
	report := f.sweep(t, requestedAt.Add(time.Minute))

	if report.Examined != 0 {
		t.Errorf("Examined = %d, want 0 — nothing is stalled yet", report.Examined)
	}
	if got := f.transferState(t, "tx-1").Status(); got != transferdomain.StatusRequested {
		t.Errorf("Status = %q, want requested — untouched", got)
	}
}

// TestGivingUpBeforeAnyMoneyMovedFails checks the safe half of the state-aware
// give-up: nothing left the source wallet, so failing is honest and the
// wallets are whole.
func TestGivingUpBeforeAnyMoneyMovedFails(t *testing.T) {
	f := newFixture(t, sweeper.Options{MaxAttempts: 2})
	from := f.wallet(t, "EUR", 10000)
	to := f.wallet(t, "EUR", 0)
	f.requestTransfer(t, "tx-1", from, to, 5000, "EUR")

	// Force the attempt budget to run out without the transfer progressing, by
	// recording attempts directly — the same thing a repeatedly-failing
	// re-drive would produce.
	for i := 0; i < 2; i++ {
		if err := f.reactor.RecordAttempt(context.Background(), "tx-1"); err != nil {
			t.Fatalf("recording attempt: %v", err)
		}
	}

	f.sweep(t, muchLater)

	agg := f.transferState(t, "tx-1")
	if agg.Status() != transferdomain.StatusFailed {
		t.Fatalf("Status = %q, want failed — no money moved, so failing is safe", agg.Status())
	}
	if agg.FailureReason() != "timed_out" {
		t.Errorf("FailureReason = %q, want timed_out", agg.FailureReason())
	}
	if got := f.balance(t, from); got != 10000 {
		t.Errorf("source balance = %d, want 10000 — untouched", got)
	}
}

// TestGivingUpAfterTheDebitIsStuckNotFailed is the half that matters. The
// money is out of the source wallet. Writing "failed" here would be a lie —
// failed means the wallets are whole — and would hide a real loss.
func TestGivingUpAfterTheDebitIsStuckNotFailed(t *testing.T) {
	f := newFixture(t, sweeper.Options{MaxAttempts: 2})
	from := f.wallet(t, "EUR", 10000)
	to := f.wallet(t, "EUR", 0)
	f.requestTransfer(t, "tx-1", from, to, 5000, "EUR")

	// Get it to "debited": the money is now out.
	if err := f.reactor.Redrive(context.Background(), "tx-1"); err != nil {
		t.Fatalf("redrive: %v", err)
	}
	if got := f.transferState(t, "tx-1").Status(); got != transferdomain.StatusDebited {
		t.Fatalf("setup: Status = %q, want debited", got)
	}

	for i := 0; i < 2; i++ {
		if err := f.reactor.RecordAttempt(context.Background(), "tx-1"); err != nil {
			t.Fatalf("recording attempt: %v", err)
		}
	}

	f.sweep(t, muchLater)

	agg := f.transferState(t, "tx-1")
	if agg.Status() == transferdomain.StatusFailed {
		t.Fatal("MUST NOT be failed — 5000 left the source wallet, so the wallets are not whole")
	}
	if agg.Status() != transferdomain.StatusStuck {
		t.Fatalf("Status = %q, want stuck", agg.Status())
	}
	if agg.FailureReason() != "timed_out" {
		t.Errorf("FailureReason = %q, want timed_out", agg.FailureReason())
	}
}

func TestSweepingASettledTransferDoesNothing(t *testing.T) {
	f := newFixture(t, sweeper.Options{})
	from := f.wallet(t, "EUR", 10000)
	to := f.wallet(t, "EUR", 0)
	f.requestTransfer(t, "tx-1", from, to, 5000, "EUR")

	for i := 0; i < 3 && !f.transferState(t, "tx-1").IsTerminal(); i++ {
		f.sweep(t, muchLater)
	}
	wantFrom, wantTo := f.balance(t, from), f.balance(t, to)

	report := f.sweep(t, muchLater.Add(time.Hour))

	if report.Examined != 0 {
		t.Errorf("Examined = %d, want 0 — a settled transfer is not in flight", report.Examined)
	}
	if f.balance(t, from) != wantFrom || f.balance(t, to) != wantTo {
		t.Error("sweeping a settled transfer must not move money")
	}
}

func TestAProcessKindWithNoDriverIsReportedNotIgnored(t *testing.T) {
	views := projections.NewMemoryProcessStore()
	if err := views.Upsert(context.Background(), &projections.ProcessView{
		Kind: "something_new", ID: "x", Status: "requested", RequestedAt: requestedAt,
	}); err != nil {
		t.Fatalf("seeding view: %v", err)
	}

	s := sweeper.New(views, map[string]sweeper.Driver{}, sweeper.Options{}, nil)
	report, err := s.Sweep(context.Background(), muchLater)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if report.Unhandled != 1 {
		t.Errorf("Unhandled = %d, want 1 — an unwired process kind stalls forever and must be loud", report.Unhandled)
	}
}
