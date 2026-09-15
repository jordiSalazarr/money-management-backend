package projector_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/projector"
	"github.com/jordisalazar/money-management/internal/shared/database"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// countingSubscriber records what it saw and can be told to start failing.
type countingSubscriber struct {
	seen    []string
	failOn  string
	failure error
}

func (c *countingSubscriber) Handle(event any, _ eventstore.EventMetadata) error {
	name := ""
	if e, ok := event.(eventstore.DomainEvent); ok {
		name = e.EventType()
	}
	if c.failOn != "" && name == c.failOn {
		return c.failure
	}
	c.seen = append(c.seen, name)
	return nil
}

func seedWallet(t *testing.T, store *eventstore.MemoryStore, codec *eventstore.Codec, id string, events ...eventstore.DomainEvent) {
	t.Helper()
	stream := eventstore.StreamID{Category: "wallet", ID: id}
	recorded, _ := store.Load(context.Background(), stream)
	err := eventstore.AppendChanges(context.Background(), store, codec, stream,
		eventstore.ExpectedRevisionAfter(recorded), events, "corr-1",
		time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}
}

// runUntilCaughtUp runs the projection over everything currently in the log,
// then stops.
//
// A real runner stays subscribed forever, which is the point of it — so the
// test bounds it with a deadline instead. The window only has to cover an
// in-memory catch-up, hence the short one; a deadline reached is success here,
// not failure.
func runUntilCaughtUp(t *testing.T, r *projector.Runner) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := r.Run(ctx)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func TestAProjectionResumesFromItsCheckpoint(t *testing.T) {
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	seedWallet(t, store, codec, "w1",
		wallet.WalletAdded{WalletID: "w1", UserID: "u1", Currency: "EUR", CreatedAt: now},
		wallet.MoneyDeposited{WalletID: "w1", Amount: 100, Currency: "EUR", DepositedAt: now},
	)

	checkpoints := projector.NewMemoryCheckpoints()
	first := &countingSubscriber{}
	def := projector.Projection{
		Name:  "test_projector",
		Build: func(database.DBTX) eventstore.Subscriber { return first },
	}
	if err := runUntilCaughtUp(t, projector.NewRunner(def, store, codec, checkpoints, projector.NoTransactor{}, nil)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(first.seen) != 2 {
		t.Fatalf("first run saw %d events, want 2", len(first.seen))
	}

	// More events arrive, then a fresh runner starts with the same checkpoint.
	seedWallet(t, store, codec, "w1",
		wallet.MoneyWithdrawn{WalletID: "w1", Amount: 50, Currency: "EUR", WithdrawnAt: now})

	second := &countingSubscriber{}
	def.Build = func(database.DBTX) eventstore.Subscriber { return second }
	if err := runUntilCaughtUp(t, projector.NewRunner(def, store, codec, checkpoints, projector.NoTransactor{}, nil)); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if len(second.seen) != 1 {
		t.Fatalf("second run saw %d events, want 1 — it must resume, not replay", len(second.seen))
	}
	if second.seen[0] != "MoneyWithdrawn" {
		t.Errorf("resumed at %q, want MoneyWithdrawn", second.seen[0])
	}
}

// TestAFailedProjectionDoesNotAdvanceItsCheckpoint is the property the
// whole transaction exists for. If the position moved past an event the
// projection could not apply, the gap would be permanent and silent.
func TestAFailedProjectionDoesNotAdvanceItsCheckpoint(t *testing.T) {
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	seedWallet(t, store, codec, "w1",
		wallet.WalletAdded{WalletID: "w1", UserID: "u1", Currency: "EUR", CreatedAt: now},
		wallet.MoneyDeposited{WalletID: "w1", Amount: 100, Currency: "EUR", DepositedAt: now},
	)

	checkpoints := projector.NewMemoryCheckpoints()
	boom := errors.New("postgres is down")
	failing := &countingSubscriber{failOn: "MoneyDeposited", failure: boom}

	err := projector.NewRunner(projector.Projection{
		Name:  "test_projector",
		Build: func(database.DBTX) eventstore.Subscriber { return failing },
	}, store, codec, checkpoints, projector.NoTransactor{}, nil).Run(context.Background())

	if !errors.Is(err, boom) {
		t.Fatalf("want the projection's error, got %v", err)
	}

	// The checkpoint must sit at the event BEFORE the failure, so a restart
	// retries the one it choked on.
	at, err := checkpoints.Load(context.Background(), "test_projector")
	if err != nil {
		t.Fatalf("loading checkpoint: %v", err)
	}

	retry := &countingSubscriber{}
	if err := runUntilCaughtUp(t, projector.NewRunner(projector.Projection{
		Name:  "test_projector",
		Build: func(database.DBTX) eventstore.Subscriber { return retry },
	}, store, codec, checkpoints, projector.NoTransactor{}, nil)); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if len(retry.seen) != 1 || retry.seen[0] != "MoneyDeposited" {
		t.Fatalf("after a failure the retry must re-apply the failed event, saw %v (checkpoint at commit %d)", retry.seen, at.Commit)
	}
}

// TestProjectionsCheckpointIndependently is ADR 0003's actual claim: one can
// be rebuilt without disturbing the others.
func TestProjectionsCheckpointIndependently(t *testing.T) {
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	seedWallet(t, store, codec, "w1",
		wallet.WalletAdded{WalletID: "w1", UserID: "u1", Currency: "EUR", CreatedAt: now},
		wallet.MoneyDeposited{WalletID: "w1", Amount: 100, Currency: "EUR", DepositedAt: now},
	)

	checkpoints := projector.NewMemoryCheckpoints()
	run := func(name string, sub eventstore.Subscriber) {
		t.Helper()
		if err := runUntilCaughtUp(t, projector.NewRunner(projector.Projection{
			Name:  name,
			Build: func(database.DBTX) eventstore.Subscriber { return sub },
		}, store, codec, checkpoints, projector.NoTransactor{}, nil)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	run("alpha", &countingSubscriber{})
	run("beta", &countingSubscriber{})

	// Rebuild alpha only — which is what "delete its checkpoint row" means.
	if err := checkpoints.Save(context.Background(), nil, "alpha", eventstore.Start); err != nil {
		t.Fatalf("resetting alpha: %v", err)
	}

	alpha, beta := &countingSubscriber{}, &countingSubscriber{}
	run("alpha", alpha)
	run("beta", beta)

	if len(alpha.seen) != 2 {
		t.Errorf("alpha saw %d events, want 2 — it was reset and must replay", len(alpha.seen))
	}
	if len(beta.seen) != 0 {
		t.Errorf("beta saw %d events, want 0 — rebuilding alpha must not disturb it", len(beta.seen))
	}
}

// TestConcurrentRunnersShareACheckpointStore reproduces what production does
// and the other tests do not: five projections, five goroutines, one
// checkpoint store between them.
//
// The single-runner tests all passed while MemoryCheckpoints held an
// unsynchronised map, and `go test -race` never saw it either — the race only
// exists when runners actually run at the same time.
func TestConcurrentRunnersShareACheckpointStore(t *testing.T) {
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	seedWallet(t, store, codec, "w1",
		wallet.WalletAdded{WalletID: "w1", UserID: "u1", Currency: "EUR", CreatedAt: now},
		wallet.MoneyDeposited{WalletID: "w1", Amount: 100, Currency: "EUR", DepositedAt: now},
		wallet.MoneyWithdrawn{WalletID: "w1", Amount: 25, Currency: "EUR", WithdrawnAt: now},
	)

	checkpoints := projector.NewMemoryCheckpoints()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("projection_%d", i)
		runner := projector.NewRunner(projector.Projection{
			Name:  name,
			Build: func(database.DBTX) eventstore.Subscriber { return &countingSubscriber{} },
		}, store, codec, checkpoints, projector.NoTransactor{}, nil)

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = runner.Run(ctx)
		}()
	}
	wg.Wait()

	// Each kept its own position, and none corrupted the others'.
	for i := 0; i < 5; i++ {
		at, err := checkpoints.Load(ctx, fmt.Sprintf("projection_%d", i))
		if err != nil {
			t.Fatalf("loading checkpoint %d: %v", i, err)
		}
		if at == eventstore.Start {
			t.Errorf("projection_%d never checkpointed", i)
		}
	}
}
