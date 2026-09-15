package transfer_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence"
	occurrencedomain "github.com/jordisalazar/money-management/internal/payments/processes/occurrence/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer"
	transferdomain "github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	walletdomain "github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/disablewallet"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// The whole point of phase 1's port split was that a saga could be built and
// debugged with no infrastructure and then work unchanged on the real thing.
// These tests are where that claim gets checked: the same processes, the same
// codec, the same command handlers — only the store differs.
//
// Skips when KURRENTDB_URL is unset. `make test-kurrent` runs them.
type kurrentFixture struct {
	store    *eventstore.KurrentStore
	codec    *eventstore.Codec
	transfer *transfer.Reactor
	occur    *occurrence.Reactor
	now      time.Time
}

func newKurrentFixture(t *testing.T) *kurrentFixture {
	t.Helper()
	url := os.Getenv("KURRENTDB_URL")
	if url == "" {
		t.Skip("KURRENTDB_URL not set — skipping KurrentDB integration tests")
	}

	store, err := eventstore.NewKurrentStore(url)
	if err != nil {
		t.Fatalf("connecting to kurrentdb: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	codec := eventcatalog.NewCodec()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	return &kurrentFixture{
		store:    store,
		codec:    codec,
		transfer: transfer.NewReactor(store, codec, clock),
		occur:    occurrence.NewReactor(store, codec, clock),
		now:      now,
	}
}

func (f *kurrentFixture) wallet(t *testing.T, currency string, balance int) string {
	t.Helper()
	id, err := addwallet.NewCommandHandler(f.store, f.codec).Handle(context.Background(), addwallet.Command{
		UserID: "user-1", Name: "w", InitialAmount: balance, Currency: currency, Category: "Cash",
	})
	if err != nil {
		t.Fatalf("creating wallet: %v", err)
	}
	return id
}

func (f *kurrentFixture) balance(t *testing.T, walletID string) int {
	t.Helper()
	agg := walletdomain.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), f.store, f.codec, streams.Wallet(walletID), agg.Apply); err != nil {
		t.Fatalf("loading wallet: %v", err)
	}
	return agg.Balance()
}

func (f *kurrentFixture) transferState(t *testing.T, id string) *transferdomain.Aggregate {
	t.Helper()
	agg := transferdomain.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), f.store, f.codec, streams.Transfer(id), agg.Apply); err != nil {
		t.Fatalf("loading transfer: %v", err)
	}
	return agg
}

// driveTransfer walks the transfer's own stream and feeds each event to the
// reactor. Reading one stream rather than $all keeps the test independent of
// whatever else is in a shared database.
func (f *kurrentFixture) driveTransfer(t *testing.T, id string) {
	t.Helper()
	for i := 0; i < 5; i++ {
		recorded, err := f.store.Load(context.Background(), streams.Transfer(id))
		if err != nil {
			t.Fatalf("loading transfer stream: %v", err)
		}
		for _, r := range recorded {
			decoded, err := f.codec.Decode(r)
			if err != nil {
				t.Fatalf("decoding %s: %v", r.EventType, err)
			}
			if err := f.transfer.Handle(context.Background(), decoded, r.Meta()); err != nil {
				t.Fatalf("reactor on %s: %v", r.EventType, err)
			}
		}
		if f.transferState(t, id).IsTerminal() {
			return
		}
	}
	t.Fatalf("transfer %s never settled (stuck at %q)", id, f.transferState(t, id).Status())
}

func TestTheSagaRunsOnKurrentDB(t *testing.T) {
	f := newKurrentFixture(t)
	from := f.wallet(t, "EUR", 10000)
	to := f.wallet(t, "EUR", 0)

	id := uuid.NewString()
	agg, err := transferdomain.Request(id, "user-1", from, to, 5000, "EUR", "rent", f.now)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := eventstore.AppendChanges(context.Background(), f.store, f.codec,
		streams.Transfer(id), eventstore.NoStream, agg.Changes(), "corr-1", f.now); err != nil {
		t.Fatalf("appending: %v", err)
	}

	f.driveTransfer(t, id)

	if got := f.transferState(t, id).Status(); got != transferdomain.StatusCompleted {
		t.Fatalf("Status = %q, want completed", got)
	}
	if got := f.balance(t, from); got != 5000 {
		t.Errorf("source balance = %d, want 5000", got)
	}
	if got := f.balance(t, to); got != 5000 {
		t.Errorf("destination balance = %d, want 5000", got)
	}
}

// The compensation path, against the real database: the destination is
// disabled in the window after the transfer is accepted and before the credit
// runs, so wallet.Refund has to put the money back.
func TestCompensationRunsOnKurrentDB(t *testing.T) {
	f := newKurrentFixture(t)
	from := f.wallet(t, "EUR", 5000)
	to := f.wallet(t, "EUR", 0)

	id := uuid.NewString()
	agg, _ := transferdomain.Request(id, "user-1", from, to, 5000, "EUR", "rent", f.now)
	if err := eventstore.AppendChanges(context.Background(), f.store, f.codec,
		streams.Transfer(id), eventstore.NoStream, agg.Changes(), "corr-1", f.now); err != nil {
		t.Fatalf("appending: %v", err)
	}

	disable := disablewallet.NewCommandHandler(f.store, f.codec)
	for _, w := range []string{to, from} {
		if err := disable.Handle(context.Background(), disablewallet.Command{
			WalletID: w, UserID: "user-1", Reason: "closing", DisabledAt: f.now,
		}); err != nil {
			t.Fatalf("disabling %s: %v", w, err)
		}
	}

	f.driveTransfer(t, id)

	state := f.transferState(t, id)
	if state.Status() != transferdomain.StatusFailed {
		t.Fatalf("Status = %q, want failed — the refund must have succeeded", state.Status())
	}
	if got := f.balance(t, from); got != 5000 {
		t.Fatalf("source balance = %d, want 5000 — THE MONEY MUST COME BACK", got)
	}
}

// TestTheStreamNameIsTheIdempotencyKeyOnKurrentDB is the claim ADR 0002 makes
// about occurrences, checked against the database that actually enforces it:
// the second append for a date collides because the stream already exists.
func TestTheStreamNameIsTheIdempotencyKeyOnKurrentDB(t *testing.T) {
	f := newKurrentFixture(t)
	w := f.wallet(t, "EUR", 50000)

	rmID := uuid.NewString()
	forDate := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	stream := streams.Occurrence(rmID, forDate)

	due := func() error {
		agg, err := occurrencedomain.Due(stream.ID, rmID, w, "user-1", "withdrawal", 9900, "EUR", "gym", forDate, f.now)
		if err != nil {
			return err
		}
		return eventstore.AppendChanges(context.Background(), f.store, f.codec,
			stream, eventstore.NoStream, agg.Changes(), "corr-1", f.now)
	}

	if err := due(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := due(); err != eventstore.ErrConcurrencyConflict {
		t.Fatalf("second run for the same date: want ErrConcurrencyConflict, got %v", err)
	}
}
