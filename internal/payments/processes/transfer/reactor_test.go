package transfer_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer"
	transferdomain "github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/slices/requesttransfer"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	walletdomain "github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/disablewallet"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

var at = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

var errCaughtUp = errors.New("caught up")

type fixture struct {
	store   *eventstore.MemoryStore
	codec   *eventstore.Codec
	reactor *transfer.Reactor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	return &fixture{
		store:   store,
		codec:   codec,
		reactor: transfer.NewReactor(store, codec, func() time.Time { return at }),
	}
}

func (f *fixture) newWallet(t *testing.T, currency string, balance int) string {
	t.Helper()
	id, err := addwallet.NewCommandHandler(f.store, f.codec).Handle(context.Background(), addwallet.Command{
		UserID: "user-1", Name: "w", InitialAmount: balance, Currency: currency, Category: "Cash",
	})
	if err != nil {
		t.Fatalf("creating wallet: %v", err)
	}
	return id
}

func (f *fixture) disableWallet(t *testing.T, walletID string) {
	t.Helper()
	err := disablewallet.NewCommandHandler(f.store, f.codec).Handle(context.Background(), disablewallet.Command{
		WalletID: walletID, UserID: "user-1", Reason: "closing it", DisabledAt: at,
	})
	if err != nil {
		t.Fatalf("disabling wallet: %v", err)
	}
}

// requestViaHandler opens a transfer through the real command handler, so the
// API precheck runs exactly as it would for an HTTP request.
func (f *fixture) requestViaHandler(t *testing.T, from, to string, amount int, currency string) string {
	t.Helper()
	id, err := requesttransfer.NewCommandHandler(f.store, f.codec).Handle(context.Background(),
		requesttransfer.Command{
			UserID: "user-1", FromWalletID: from, ToWalletID: to,
			Amount: amount, Currency: currency, Reason: "rent",
		})
	if err != nil {
		t.Fatalf("requesting transfer: %v", err)
	}
	return id
}

// request opens a transfer by appending directly, bypassing the precheck. Used
// only where a test needs a state the precheck would refuse to create.
func (f *fixture) request(t *testing.T, from, to string, amount int, currency string) string {
	t.Helper()
	agg, err := transferdomain.Request("tx-1", "user-1", from, to, amount, currency, "rent", at)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	err = eventstore.AppendChanges(context.Background(), f.store, f.codec,
		streams.Transfer("tx-1"), eventstore.NoStream, agg.Changes(), "corr-1", at)
	if err != nil {
		t.Fatalf("appending transfer: %v", err)
	}
	return "tx-1"
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

// drive runs the reactor over every event currently in the log, the way the
// real subscription would.
func (f *fixture) drive(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Snapshot first: handling events appends more, and we only want one pass.
	// Stopping at the head is what makes this a catch-up read rather than a
	// live subscription that would sit there until the deadline.
	head, err := f.store.Head(ctx)
	if err != nil {
		t.Fatalf("reading the log head: %v", err)
	}
	if head == eventstore.Start {
		return
	}
	var batch []eventstore.Recorded
	err = f.store.SubscribeAll(ctx, eventstore.Start, func(r eventstore.Recorded) error {
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

// driveUntilTerminal keeps passing over the log until the transfer settles,
// because each saga step appends the event that triggers the next one.
func (f *fixture) driveUntilTerminal(t *testing.T, id string) {
	t.Helper()
	for i := 0; i < 5; i++ {
		f.drive(t)
		if f.transferState(t, id).IsTerminal() {
			return
		}
	}
	t.Fatalf("transfer %s never reached a terminal state (stuck at %q)", id, f.transferState(t, id).Status())
}

func TestTheHappyPath(t *testing.T) {
	f := newFixture(t)
	from := f.newWallet(t, "EUR", 10000)
	to := f.newWallet(t, "EUR", 0)
	id := f.request(t, from, to, 5000, "EUR")

	f.driveUntilTerminal(t, id)

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

func TestRejectedBeforeTheDebit(t *testing.T) {
	f := newFixture(t)
	from := f.newWallet(t, "EUR", 100) // not enough
	to := f.newWallet(t, "EUR", 0)
	id := f.request(t, from, to, 5000, "EUR")

	f.driveUntilTerminal(t, id)

	agg := f.transferState(t, id)
	if agg.Status() != transferdomain.StatusFailed {
		t.Fatalf("Status = %q, want failed", agg.Status())
	}
	if agg.MoneyMoved() {
		t.Error("nothing should have moved")
	}
	if got := f.balance(t, from); got != 100 {
		t.Errorf("source balance = %d, want 100 — untouched", got)
	}
}

// TestTheFiftyEuroScenario is the chain from ADR 0002 and §4.4 of the
// migration plan — the one that used to lose the money outright: the credit
// leg is rejected, and the source wallet is disabled in between, so the
// compensating deposit would have been rejected too, the record dropped with
// a log.Warn, and the funds gone with no event recording it.
//
// It is driven through requesttransfer, the same entry point the HTTP handler
// uses, rather than by appending TransferRequested directly. That matters:
// the API precheck now rejects a statically-mismatched pair up front, so a
// test that bypassed it would be proving the compensation path against a
// transfer the real system would never have accepted.
//
// So the failure is arranged the way it actually happens in production — as a
// race. Both wallets are EUR and healthy when the transfer is accepted; the
// destination is disabled afterwards, in the window between the precheck and
// the credit. No precheck can close that window, which is exactly why
// compensation has to work.
func TestTheFiftyEuroScenario(t *testing.T) {
	f := newFixture(t)
	from := f.newWallet(t, "EUR", 5000)
	to := f.newWallet(t, "EUR", 0)
	id := f.requestViaHandler(t, from, to, 5000, "EUR")

	// The window opens here: accepted, not yet debited.
	f.disableWallet(t, to)

	// Step 1: the debit goes through. The money is now out of the source.
	f.drive(t)
	if got := f.transferState(t, id).Status(); got != transferdomain.StatusDebited {
		t.Fatalf("after the first pass Status = %q, want debited", got)
	}
	if got := f.balance(t, from); got != 0 {
		t.Fatalf("source balance = %d, want 0 — the debit should have happened", got)
	}

	// Step 2: the source is disabled too, so the compensating deposit runs
	// against a wallet that no longer accepts deposits. This is the step that
	// used to drop the record and lose the money.
	f.disableWallet(t, from)

	// Step 3: the credit is refused, and compensation has to work anyway.
	f.driveUntilTerminal(t, id)

	agg := f.transferState(t, id)
	if agg.Status() != transferdomain.StatusFailed {
		t.Fatalf("Status = %q, want failed — the refund must have succeeded", agg.Status())
	}
	if got := f.balance(t, from); got != 5000 {
		t.Fatalf("source balance = %d, want 5000 — THE MONEY MUST COME BACK", got)
	}
	if got := f.balance(t, to); got != 0 {
		t.Errorf("destination balance = %d, want 0", got)
	}
}

func TestRedeliveryIsANoOp(t *testing.T) {
	f := newFixture(t)
	from := f.newWallet(t, "EUR", 10000)
	to := f.newWallet(t, "EUR", 0)
	id := f.request(t, from, to, 5000, "EUR")

	f.driveUntilTerminal(t, id)
	wantFrom, wantTo := f.balance(t, from), f.balance(t, to)

	// Re-deliver everything. Persistent subscriptions are at-least-once, so
	// this is an ordinary Tuesday, not an edge case.
	f.drive(t)
	f.drive(t)

	if got := f.balance(t, from); got != wantFrom {
		t.Errorf("source balance = %d, want %d — redelivery must not debit twice", got, wantFrom)
	}
	if got := f.balance(t, to); got != wantTo {
		t.Errorf("destination balance = %d, want %d — redelivery must not credit twice", got, wantTo)
	}
	if got := f.transferState(t, id).Status(); got != transferdomain.StatusCompleted {
		t.Errorf("Status = %q, want completed", got)
	}
}

func TestEventsItDoesNotHandleAreIgnored(t *testing.T) {
	f := newFixture(t)
	f.newWallet(t, "EUR", 100)

	// WalletAdded is registered with the codec but means nothing to this
	// reactor. That is normal fan-out, not a fault.
	f.drive(t)
}
