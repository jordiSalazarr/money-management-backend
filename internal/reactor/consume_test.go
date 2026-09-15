package reactor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/reactor"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		retryCount int
		want       eventstore.Disposition
	}{
		{
			// A rule-based rejection reaches here as nil: the process already
			// recorded its terminal outcome, so the work IS done. This is the
			// case the old router got wrong — it dropped the record instead.
			name: "handled", err: nil, want: eventstore.Done,
		},
		{
			name: "transient fault retries",
			err:  errors.New("kurrentdb unreachable"), want: eventstore.Retry,
		},
		{
			// Redelivery cannot make an unregistered type known, and retrying
			// forever would block every process behind it.
			name: "unknown event type parks",
			err:  eventstore.ErrUnknownEventType, want: eventstore.Park,
		},
		{
			name: "an event from a newer build parks",
			err:  eventstore.ErrFutureSchemaVersion, want: eventstore.Park,
		},
		{
			// Whatever it claimed to be, a fault that survived this many
			// redeliveries is not transient.
			name: "a fault that will not go away eventually parks",
			err:  errors.New("still broken"), retryCount: 10, want: eventstore.Park,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reactor.Classify(c.err, c.retryCount); got != c.want {
				t.Errorf("Classify(%v, %d) = %s, want %s", c.err, c.retryCount, got, c.want)
			}
		})
	}
}

// failOnce fails the first delivery and succeeds afterwards — a transient
// fault, the case retrying exists for.
type failOnce struct {
	calls int
}

func (f *failOnce) Handle(_ context.Context, _ any, _ eventstore.EventMetadata) error {
	f.calls++
	if f.calls == 1 {
		return errors.New("postgres blinked")
	}
	return nil
}

func TestATransientFaultIsRedeliveredAndThenSucceeds(t *testing.T) {
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	seedDeposit(t, store, codec)

	handler := &failOnce{}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_ = reactor.NewRouter(codec, nil, handler).Consume(ctx, store, "test-group")

	if handler.calls < 2 {
		t.Fatalf("handler called %d times, want at least 2 — the transient fault must be redelivered", handler.calls)
	}
	if parked := store.Parked("test-group"); len(parked) != 0 {
		t.Errorf("nothing should be parked, got %d", len(parked))
	}
}

type alwaysFails struct{ err error }

func (a alwaysFails) Handle(_ context.Context, _ any, _ eventstore.EventMetadata) error { return a.err }

// TestAPoisonMessageIsParkedRatherThanBlockingForever is the property that
// makes retrying safe to bias towards. Without a park queue, one message that
// can never succeed stops every process behind it.
func TestAPoisonMessageIsParkedRatherThanBlockingForever(t *testing.T) {
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	seedDeposit(t, store, codec)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = reactor.NewRouter(codec, nil, alwaysFails{err: errors.New("permanently broken")}).
		Consume(ctx, store, "poison-group")

	parked := store.Parked("poison-group")
	if len(parked) != 1 {
		t.Fatalf("want 1 parked message, got %d", len(parked))
	}
	if parked[0].EventType != "MoneyDeposited" {
		t.Errorf("parked the wrong message: %s", parked[0].EventType)
	}
}

func seedDeposit(t *testing.T, store *eventstore.MemoryStore, codec *eventstore.Codec) {
	t.Helper()
	err := eventstore.AppendChanges(context.Background(), store, codec,
		eventstore.StreamID{Category: "wallet", ID: "w1"}, eventstore.NoStream,
		[]wallet.DomainEvent{wallet.MoneyDeposited{
			WalletID: "w1", UserID: "u1", Amount: 500, Currency: "EUR",
			Reason: "salary", Category: "Salary",
		}}, "corr-1", time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}
}
