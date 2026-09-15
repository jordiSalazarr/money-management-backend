package occurrence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence"
	occurrencedomain "github.com/jordisalazar/money-management/internal/payments/processes/occurrence/domain"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	walletdomain "github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

var (
	at      = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	forDate = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	errCaughtUp = errors.New("caught up")
)

type fixture struct {
	store   *eventstore.MemoryStore
	codec   *eventstore.Codec
	reactor *occurrence.Reactor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	return &fixture{store, codec, occurrence.NewReactor(store, codec, func() time.Time { return at })}
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

// due appends MovementDue the way the cron does: at NoStream, on a stream
// named from the recurring movement and the date.
func (f *fixture) due(t *testing.T, rmID, walletID, typ string, amount int, date time.Time) error {
	t.Helper()
	stream := streams.Occurrence(rmID, date)
	agg, err := occurrencedomain.Due(stream.ID, rmID, walletID, "user-1", typ, amount, "EUR", "gym membership", date, at)
	if err != nil {
		t.Fatalf("building occurrence: %v", err)
	}
	return eventstore.AppendChanges(context.Background(), f.store, f.codec,
		stream, eventstore.NoStream, agg.Changes(), "corr-1", at)
}

func (f *fixture) state(t *testing.T, rmID string, date time.Time) *occurrencedomain.Aggregate {
	t.Helper()
	agg := occurrencedomain.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), f.store, f.codec, streams.Occurrence(rmID, date), agg.Apply); err != nil {
		t.Fatalf("loading occurrence: %v", err)
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

func TestAStandingOrderThatCanBePaid(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 20000)
	if err := f.due(t, "rm-1", w, "withdrawal", 9900, forDate); err != nil {
		t.Fatalf("due: %v", err)
	}

	f.drive(t)

	if got := f.state(t, "rm-1", forDate).Status(); got != occurrencedomain.StatusExecuted {
		t.Fatalf("Status = %q, want executed", got)
	}
	if got := f.balance(t, w); got != 10100 {
		t.Errorf("balance = %d, want 10100", got)
	}
}

// TestAShortMonthIsSkippedNotSilentlySwallowed is the live bug, the one that
// needed no race and no outage — only a month where the balance was short.
//
// The old code appended RecurringMovementExecuted *before* attempting the
// withdrawal. The withdrawal was refused, the error classified permanent, the
// record dropped with a log.Warn. The date stayed marked executed, so the cron
// never retried it: the standing order silently skipped a month while the read
// model said it had run.
func TestAShortMonthIsSkippedNotSilentlySwallowed(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 100) // short
	if err := f.due(t, "rm-1", w, "withdrawal", 9900, forDate); err != nil {
		t.Fatalf("due: %v", err)
	}

	f.drive(t)

	agg := f.state(t, "rm-1", forDate)
	if agg.Status() != occurrencedomain.StatusSkipped {
		t.Fatalf("Status = %q, want skipped — it must be recorded, not swallowed", agg.Status())
	}
	if agg.SkipReason() != "insufficient_funds" {
		t.Errorf("SkipReason = %q, want insufficient_funds — a person is entitled to know why", agg.SkipReason())
	}
	if agg.MoneyMoved() {
		t.Error("a skipped occurrence moved no money")
	}
	if got := f.balance(t, w); got != 100 {
		t.Errorf("balance = %d, want 100 — untouched", got)
	}
}

// TestTheCronCannotFireTheSameDateTwice is the idempotency claim in ADR 0002:
// the stream name is derived from the recurring movement and the date, so a
// second run for that date collides at NoStream. No dedup table, no key.
func TestTheCronCannotFireTheSameDateTwice(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 50000)

	if err := f.due(t, "rm-1", w, "withdrawal", 9900, forDate); err != nil {
		t.Fatalf("first due: %v", err)
	}
	err := f.due(t, "rm-1", w, "withdrawal", 9900, forDate)
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Fatalf("want ErrConcurrencyConflict on a repeat run for the same date, got %v", err)
	}

	f.drive(t)
	if got := f.balance(t, w); got != 40100 {
		t.Errorf("balance = %d, want 40100 — the movement must be applied once", got)
	}
}

func TestDifferentDatesAreIndependent(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 50000)
	next := forDate.AddDate(0, 1, 0)

	if err := f.due(t, "rm-1", w, "withdrawal", 9900, forDate); err != nil {
		t.Fatalf("september: %v", err)
	}
	if err := f.due(t, "rm-1", w, "withdrawal", 9900, next); err != nil {
		t.Fatalf("october: %v", err)
	}

	f.drive(t)

	if got := f.balance(t, w); got != 30200 {
		t.Errorf("balance = %d, want 30200 — both months should have been paid", got)
	}
}

func TestRedeliveryDoesNotPayTwice(t *testing.T) {
	f := newFixture(t)
	w := f.wallet(t, 50000)
	if err := f.due(t, "rm-1", w, "withdrawal", 9900, forDate); err != nil {
		t.Fatalf("due: %v", err)
	}

	f.drive(t)
	f.drive(t)
	f.drive(t)

	if got := f.balance(t, w); got != 40100 {
		t.Errorf("balance = %d, want 40100 — redelivery must not pay twice", got)
	}
}
