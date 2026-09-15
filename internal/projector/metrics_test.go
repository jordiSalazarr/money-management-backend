package projector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/projector"
	"github.com/jordisalazar/money-management/internal/shared/database"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

func TestLagIsTheDistanceFromTheHead(t *testing.T) {
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
	reporter := projector.NewLagReporter(checkpoints, store, []string{"behind", "caught_up"})

	// "caught_up" has read everything; "behind" has read nothing.
	if err := runUntilCaughtUp(t, projector.NewRunner(projector.Projection{
		Name:  "caught_up",
		Build: func(database.DBTX) eventstore.Subscriber { return &countingSubscriber{} },
	}, store, codec, checkpoints, projector.NoTransactor{}, nil)); err != nil {
		t.Fatalf("running: %v", err)
	}

	lags, err := reporter.Measure(context.Background())
	if err != nil {
		t.Fatalf("measure: %v", err)
	}

	byName := map[string]projector.Lag{}
	for _, l := range lags {
		byName[l.Projection] = l
	}

	if !byName["caught_up"].CaughtUp {
		t.Errorf("caught_up should be caught up, behind by %d", byName["caught_up"].Behind)
	}
	if byName["behind"].CaughtUp {
		t.Error("behind has read nothing and must not report caught up")
	}
	if byName["behind"].Behind == 0 {
		t.Error("behind should report a non-zero distance from the head")
	}
}

func TestMetricsExposeEveryProjection(t *testing.T) {
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()

	seedWallet(t, store, codec, "w1", wallet.WalletAdded{
		WalletID: "w1", UserID: "u1", Currency: "EUR",
		CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	})

	reporter := projector.NewLagReporter(projector.NewMemoryCheckpoints(), store,
		[]string{"wallets_projector", "movements_projector"})

	w := httptest.NewRecorder()
	reporter.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`projection_lag_positions{projection="wallets_projector"}`,
		`projection_lag_positions{projection="movements_projector"}`,
		`projection_checkpoint_position{projection="wallets_projector"}`,
		"projection_log_head_position",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s\ngot:\n%s", want, body)
		}
	}
}

// Being behind is not a reason to restart a projector — restarting one that is
// behind only makes it further behind. Liveness fails only when the event
// store itself cannot be reached.
func TestLivenessDoesNotFailJustBecauseAProjectionIsBehind(t *testing.T) {
	store := eventstore.NewMemoryStore()
	t.Cleanup(store.Close)
	codec := eventcatalog.NewCodec()

	seedWallet(t, store, codec, "w1", wallet.WalletAdded{
		WalletID: "w1", UserID: "u1", Currency: "EUR",
		CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	})

	// A projection that has read nothing at all.
	reporter := projector.NewLagReporter(projector.NewMemoryCheckpoints(), store, []string{"very_behind"})

	w := httptest.NewRecorder()
	reporter.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — lag is not a liveness failure", w.Code)
	}
}
