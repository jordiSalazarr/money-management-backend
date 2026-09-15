package eventstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// RunStoreContract is the shared behavioural contract every event store
// implementation must satisfy on the write side. Phase 1 runs it against the
// memory store and (env-gated) DynamoDB; phase 2 runs the identical table
// against a real KurrentDB container.
//
// newStore must return a fresh, empty store on each call.
func RunStoreContract(t *testing.T, name string, newStore func(t *testing.T) eventstore.Store) {
	t.Helper()

	t.Run(name+"/append to a new stream and read it back", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		stream := eventstore.StreamID{Category: "wallet", ID: "w1"}

		err := store.Append(ctx, stream, []eventstore.Event{
			ev("WalletAdded", `{"wallet_id":"w1"}`),
			ev("MoneyDeposited", `{"amount":100}`),
		}, eventstore.NoStream)
		if err != nil {
			t.Fatalf("append to new stream: %v", err)
		}

		got, err := store.Load(ctx, stream)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("want 2 events, got %d", len(got))
		}
		if got[0].EventType != "WalletAdded" || got[1].EventType != "MoneyDeposited" {
			t.Errorf("events came back out of order: %s, %s", got[0].EventType, got[1].EventType)
		}
		// Revisions are 0-based and assigned by the store, not the caller.
		if got[0].Revision != 0 || got[1].Revision != 1 {
			t.Errorf("want revisions 0,1 got %d,%d", got[0].Revision, got[1].Revision)
		}
		if got[0].Stream != stream {
			t.Errorf("want stream %v, got %v", stream, got[0].Stream)
		}
	})

	t.Run(name+"/NoStream against an existing stream conflicts", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		stream := eventstore.StreamID{Category: "wallet", ID: "w1"}

		mustAppend(t, store, stream, eventstore.NoStream, ev("WalletAdded", `{}`))

		err := store.Append(ctx, stream, []eventstore.Event{ev("WalletAdded", `{}`)}, eventstore.NoStream)
		if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
			t.Fatalf("want ErrConcurrencyConflict, got %v", err)
		}
	})

	t.Run(name+"/append at the current revision succeeds", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		stream := eventstore.StreamID{Category: "wallet", ID: "w1"}

		mustAppend(t, store, stream, eventstore.NoStream, ev("WalletAdded", `{}`))

		loaded, err := store.Load(ctx, stream)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if err := store.Append(ctx, stream,
			[]eventstore.Event{ev("MoneyDeposited", `{}`)},
			eventstore.ExpectedRevisionAfter(loaded)); err != nil {
			t.Fatalf("append at current revision: %v", err)
		}
	})

	t.Run(name+"/append at a stale revision conflicts", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		stream := eventstore.StreamID{Category: "wallet", ID: "w1"}

		mustAppend(t, store, stream, eventstore.NoStream, ev("WalletAdded", `{}`))
		mustAppend(t, store, stream, 0, ev("MoneyDeposited", `{}`))

		// Revision 0 is now stale; the stream is at 1.
		err := store.Append(ctx, stream, []eventstore.Event{ev("MoneyWithdrawn", `{}`)}, 0)
		if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
			t.Fatalf("want ErrConcurrencyConflict, got %v", err)
		}
	})

	t.Run(name+"/a multi-event append is all or nothing", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		stream := eventstore.StreamID{Category: "wallet", ID: "w1"}

		mustAppend(t, store, stream, eventstore.NoStream, ev("WalletAdded", `{}`))

		// Stale revision, so neither event may land.
		err := store.Append(ctx, stream, []eventstore.Event{
			ev("MoneyDeposited", `{}`),
			ev("MoneyWithdrawn", `{}`),
		}, eventstore.NoStream)
		if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
			t.Fatalf("want ErrConcurrencyConflict, got %v", err)
		}

		got, err := store.Load(ctx, stream)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("rejected append leaked events: want 1, got %d", len(got))
		}
	})

	t.Run(name+"/loading a stream that does not exist", func(t *testing.T) {
		store := newStore(t)
		_, err := store.Load(context.Background(), eventstore.StreamID{Category: "wallet", ID: "nope"})
		if !errors.Is(err, eventstore.ErrAggregateNotFound) {
			t.Fatalf("want ErrAggregateNotFound, got %v", err)
		}
	})

	t.Run(name+"/streams are independent", func(t *testing.T) {
		store := newStore(t)
		a := eventstore.StreamID{Category: "wallet", ID: "a"}
		b := eventstore.StreamID{Category: "wallet", ID: "b"}

		mustAppend(t, store, a, eventstore.NoStream, ev("One", `{}`))
		// b is untouched by a's append, so it is still at NoStream.
		mustAppend(t, store, b, eventstore.NoStream, ev("Two", `{}`))

		got, err := store.Load(context.Background(), b)
		if err != nil {
			t.Fatalf("load b: %v", err)
		}
		if len(got) != 1 || got[0].Revision != 0 {
			t.Fatalf("stream b should hold exactly one event at revision 0, got %d events", len(got))
		}
	})

	t.Run(name+"/payload and metadata survive a round trip", func(t *testing.T) {
		store := newStore(t)
		stream := eventstore.StreamID{Category: "wallet", ID: "w1"}

		e := ev("MoneyDeposited", `{"amount":4200}`)
		e.CorrelationID = "corr-1"
		e.SchemaVersion = 3
		mustAppend(t, store, stream, eventstore.NoStream, e)

		got, err := store.Load(context.Background(), stream)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if string(got[0].Payload) != `{"amount":4200}` {
			t.Errorf("payload mangled: %s", got[0].Payload)
		}
		if got[0].CorrelationID != "corr-1" {
			t.Errorf("want correlation_id corr-1, got %q", got[0].CorrelationID)
		}
		if got[0].SchemaVersion != 3 {
			t.Errorf("want schema_version 3, got %d", got[0].SchemaVersion)
		}
	})

	t.Run(name+"/ExpectedRevisionAfter an empty load is NoStream", func(t *testing.T) {
		if got := eventstore.ExpectedRevisionAfter(nil); got != eventstore.NoStream {
			t.Fatalf("want NoStream (%d), got %d", eventstore.NoStream, got)
		}
	})
}

// RunFeedContract covers the read side. Only stores with a global ordered log
// implement Feed — DynamoStore does not, which is why this is a separate table.
func RunFeedContract(t *testing.T, name string, newStore func(t *testing.T) (eventstore.Store, eventstore.Feed)) {
	t.Helper()

	t.Run(name+"/the feed preserves global append order across streams", func(t *testing.T) {
		store, feed := newStore(t)
		a := eventstore.StreamID{Category: "wallet", ID: "a"}
		b := eventstore.StreamID{Category: "wallet", ID: "b"}

		mustAppend(t, store, a, eventstore.NoStream, ev("One", `{}`))
		mustAppend(t, store, b, eventstore.NoStream, ev("Two", `{}`))
		mustAppend(t, store, a, 0, ev("Three", `{}`))

		got := collect(t, feed, eventstore.Start, 3)
		for i, want := range []string{"One", "Two", "Three"} {
			if got[i].EventType != want {
				t.Errorf("position %d: want %s, got %s", i, want, got[i].EventType)
			}
		}
	})

	t.Run(name+"/the feed resumes after a position", func(t *testing.T) {
		store, feed := newStore(t)
		s := eventstore.StreamID{Category: "wallet", ID: "a"}

		mustAppend(t, store, s, eventstore.NoStream, ev("One", `{}`))
		mustAppend(t, store, s, 0, ev("Two", `{}`))
		mustAppend(t, store, s, 1, ev("Three", `{}`))

		all := collect(t, feed, eventstore.Start, 3)

		// Resuming from the first event's position yields only what follows it.
		got := collect(t, feed, all[0].Position, 2)
		if got[0].EventType != "Two" || got[1].EventType != "Three" {
			t.Fatalf("want Two,Three after resume; got %s,%s", got[0].EventType, got[1].EventType)
		}
	})

	t.Run(name+"/the feed carries the stream and revision of each event", func(t *testing.T) {
		store, feed := newStore(t)
		s := eventstore.StreamID{Category: "wallet", ID: "a"}

		mustAppend(t, store, s, eventstore.NoStream, ev("One", `{}`))
		mustAppend(t, store, s, 0, ev("Two", `{}`))

		got := collect(t, feed, eventstore.Start, 2)
		if got[1].Stream != s {
			t.Errorf("want stream %v, got %v", s, got[1].Stream)
		}
		if got[1].Revision != 1 {
			t.Errorf("want revision 1, got %d", got[1].Revision)
		}
	})

	t.Run(name+"/the feed delivers events appended after it started", func(t *testing.T) {
		store, feed := newStore(t)
		s := eventstore.StreamID{Category: "wallet", ID: "a"}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		received := make(chan eventstore.Recorded, 1)
		go func() {
			_ = feed.SubscribeAll(ctx, eventstore.Start, func(r eventstore.Recorded) error {
				received <- r
				return errStopFeed
			})
		}()

		// Appended only after the subscription is live, so this exercises the
		// live path rather than the catch-up path.
		mustAppend(t, store, s, eventstore.NoStream, ev("Late", `{}`))

		select {
		case r := <-received:
			if r.EventType != "Late" {
				t.Fatalf("want Late, got %s", r.EventType)
			}
		case <-ctx.Done():
			t.Fatal("subscriber never received the event appended after it started")
		}
	})
}

func TestMemoryStoreSatisfiesTheContract(t *testing.T) {
	RunStoreContract(t, "memory", func(t *testing.T) eventstore.Store {
		return eventstore.NewMemoryStore()
	})
	RunFeedContract(t, "memory", func(t *testing.T) (eventstore.Store, eventstore.Feed) {
		s := eventstore.NewMemoryStore()
		t.Cleanup(s.Close)
		return s, s
	})
}

// --- helpers ---

var errStopFeed = errors.New("stop")

func ev(eventType, payload string) eventstore.Event {
	return eventstore.Event{
		EventID:    eventType + "-" + payload,
		EventType:  eventType,
		Payload:    json.RawMessage(payload),
		OccurredAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	}
}

func mustAppend(t *testing.T, store eventstore.Store, stream eventstore.StreamID, expected int, events ...eventstore.Event) {
	t.Helper()
	if err := store.Append(context.Background(), stream, events, expected); err != nil {
		t.Fatalf("append to %s at revision %d: %v", stream, expected, err)
	}
}

// collect reads exactly n events from the feed starting at from, then stops
// the subscription. It fails the test rather than blocking if fewer arrive.
func collect(t *testing.T, feed eventstore.Feed, from eventstore.Position, n int) []eventstore.Recorded {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got []eventstore.Recorded
	err := feed.SubscribeAll(ctx, from, func(r eventstore.Recorded) error {
		got = append(got, r)
		if len(got) == n {
			return errStopFeed
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopFeed) {
		t.Fatalf("subscribe: %v (collected %d of %d)", err, len(got), n)
	}
	if len(got) != n {
		t.Fatalf("want %d events, got %d", n, len(got))
	}
	return got
}
