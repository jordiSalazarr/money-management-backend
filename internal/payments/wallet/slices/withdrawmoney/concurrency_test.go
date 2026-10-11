package withdrawmoney_test

// Regression specs for w-0ffdd669: a withdrawal that loses an optimistic
// concurrency race must never append changes computed against a superseded
// revision. Each scenario injects a competing write between the handler's
// Load and its Append, which is exactly when the bug bites.
//
// The chosen fix (owner decision, attempt 2 of specify) is a proper retry:
// on every ErrConcurrencyConflict the handler reloads the wallet, re-runs
// Withdraw against the fresh state (so every invariant is evaluated again),
// and appends at the fresh revision. It makes at most 3 attempts in total.
// When all 3 conflict it returns an error wrapping
// eventstore.ErrConcurrencyConflict, which the HTTP handler maps to
// 409 {"error_code":"concurrency_conflict"}. The balance it returns is the
// balance of the aggregate whose changes were actually appended.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	wallet "github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/disablewallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

const raceWalletID = "11111111-1111-1111-1111-111111111111"

// interleavingStore runs `before` exactly once, just before the first Append
// goes through: a competing write that lands between Load and Append.
type interleavingStore struct {
	eventstore.Store
	once   sync.Once
	before func()
}

func (s *interleavingStore) Append(ctx context.Context, st eventstore.StreamID, ev []eventstore.Event, exp int) error {
	s.once.Do(s.before)
	return s.Store.Append(ctx, st, ev, exp)
}

// givenWalletWith10000 creates a EUR wallet holding 10000 cents.
func givenWalletWith10000(t *testing.T) (*eventstore.MemoryStore, *eventstore.Codec, string) {
	t.Helper()
	store := eventstore.NewMemoryStore()
	codec := eventcatalog.NewCodec()
	id, err := addwallet.NewCommandHandler(store, codec).Handle(context.Background(), addwallet.Command{
		WalletID: raceWalletID, UserID: "u", Name: "w",
		InitialAmount: 10000, Currency: "EUR", Category: "Cash"})
	if err != nil {
		t.Fatalf("given wallet: %v", err)
	}
	return store, codec, id
}

// storedBalances returns the balance from replaying the stream and the
// balance the wallets projection derives from the same log.
func storedBalances(t *testing.T, store eventstore.Store, codec *eventstore.Codec, id string) (aggregate, projected int) {
	t.Helper()
	agg := wallet.NewAggregate()
	if _, err := eventstore.Rehydrate(context.Background(), store, codec, streams.Wallet(id), agg.Apply); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	ps := projections.NewMemoryWalletStore()
	p := projections.NewWalletsProjector(ps)
	recs, err := store.Load(context.Background(), streams.Wallet(id))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	evs, err := codec.DecodeAll(recs)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i, e := range evs {
		if err := p.Handle(e, recs[i].Meta()); err != nil {
			t.Fatalf("project: %v", err)
		}
	}
	v, err := ps.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	return agg.Balance(), v.CurrentBalance
}

func withdrawCmd(id string, amount int, key string) withdrawmoney.Command {
	return withdrawmoney.Command{WalletID: id, UserID: "u", Amount: amount, Currency: "EUR",
		Reason: "r", Category: "Other", IdempotencyKey: key}
}

// Given a wallet with 10000
// When two withdrawals of 8000 race (the competitor lands between Load and Append)
// Then the retry re-runs Withdraw against the fresh balance (2000) and refuses
// with ErrInsufficientFunds; the balance never goes negative.
func TestWithdrawConcurrency_Overdraft(t *testing.T) {
	store, codec, id := givenWalletWith10000(t)
	competitor := withdrawmoney.NewCommandHandler(store, codec)
	racy := &interleavingStore{Store: store, before: func() {
		if _, err := competitor.Handle(context.Background(), withdrawCmd(id, 8000, "")); err != nil {
			t.Errorf("competing withdrawal: %v", err)
		}
	}}

	reported, err := withdrawmoney.NewCommandHandler(racy, codec).Handle(context.Background(), withdrawCmd(id, 8000, ""))

	agg, view := storedBalances(t, store, codec, id)
	if !errors.Is(err, wallet.ErrInsufficientFunds) {
		t.Errorf("second 8000 withdrawal from 10000: err = %v (reported balance %d); want wallet.ErrInsufficientFunds", err, reported)
	}
	if agg != 2000 {
		t.Errorf("stored balance = %d, want 2000 (only one 8000 withdrawal applied)", agg)
	}
	if view != agg {
		t.Errorf("projected balance %d != stored balance %d", view, agg)
	}
}

// Given a wallet with 10000 and a debit with idempotency key tx1:debit
// When the same keyed debit is redelivered and races the first one
// Then the retry sees the key already applied and returns ErrAlreadyProcessed;
// the money moves once: balance 7000.
func TestWithdrawConcurrency_IdempotencyBypass(t *testing.T) {
	store, codec, id := givenWalletWith10000(t)
	competitor := withdrawmoney.NewCommandHandler(store, codec)
	racy := &interleavingStore{Store: store, before: func() {
		if _, err := competitor.Handle(context.Background(), withdrawCmd(id, 3000, "tx1:debit")); err != nil {
			t.Errorf("competing keyed withdrawal: %v", err)
		}
	}}

	_, err := withdrawmoney.NewCommandHandler(racy, codec).Handle(context.Background(), withdrawCmd(id, 3000, "tx1:debit"))

	agg, view := storedBalances(t, store, codec, id)
	if !errors.Is(err, wallet.ErrAlreadyProcessed) {
		t.Errorf("redelivered tx1:debit: err = %v, want wallet.ErrAlreadyProcessed", err)
	}
	if agg != 7000 {
		t.Errorf("stored balance = %d, want 7000 (tx1:debit applied exactly once)", agg)
	}
	if view != agg {
		t.Errorf("projected balance %d != stored balance %d", view, agg)
	}
}

// Given a wallet with 10000
// When a deposit of 5000 lands between a 1000 withdrawal's Load and Append
// Then the retry succeeds against the fresh state and reports the balance it
// actually stored (14000).
func TestWithdrawConcurrency_StaleBalanceInResponse(t *testing.T) {
	store, codec, id := givenWalletWith10000(t)
	dep := depositmoney.NewCommandHandler(store, codec)
	racy := &interleavingStore{Store: store, before: func() {
		if _, err := dep.Handle(context.Background(), depositmoney.Command{WalletID: id, UserID: "u",
			Amount: 5000, Currency: "EUR", Reason: "r", Category: "Salary"}); err != nil {
			t.Errorf("competing deposit: %v", err)
		}
	}}

	reported, err := withdrawmoney.NewCommandHandler(racy, codec).Handle(context.Background(), withdrawCmd(id, 1000, ""))

	agg, view := storedBalances(t, store, codec, id)
	if err != nil {
		t.Fatalf("withdrawal after a competing deposit: err = %v, want success after retry", err)
	}
	if reported != agg {
		t.Errorf("withdrawal reported balance %d, but stored balance is %d", reported, agg)
	}
	if agg != 14000 {
		t.Errorf("stored balance = %d, want 14000", agg)
	}
	if view != agg {
		t.Errorf("projected balance %d != stored balance %d", view, agg)
	}
}

// Given a wallet with 10000
// When the wallet is disabled between a 1000 withdrawal's Load and Append
// Then the retry re-runs Withdraw against the disabled wallet and refuses with
// ErrWalletDisabled; nothing is debited.
func TestWithdrawConcurrency_DisabledDuringRetry(t *testing.T) {
	store, codec, id := givenWalletWith10000(t)
	disable := disablewallet.NewCommandHandler(store, codec)
	racy := &interleavingStore{Store: store, before: func() {
		if err := disable.Handle(context.Background(), disablewallet.Command{WalletID: id, UserID: "u",
			Reason: "closed", DisabledAt: time.Now().UTC()}); err != nil {
			t.Errorf("competing disable: %v", err)
		}
	}}

	_, err := withdrawmoney.NewCommandHandler(racy, codec).Handle(context.Background(), withdrawCmd(id, 1000, ""))

	agg, view := storedBalances(t, store, codec, id)
	if !errors.Is(err, wallet.ErrWalletDisabled) {
		t.Errorf("withdrawal from a wallet disabled mid-flight: err = %v, want wallet.ErrWalletDisabled", err)
	}
	if agg != 10000 {
		t.Errorf("stored balance = %d, want 10000 (nothing debited from a disabled wallet)", agg)
	}
	if view != agg {
		t.Errorf("projected balance %d != stored balance %d", view, agg)
	}
}

// Given a wallet with 10000
// When two withdrawals of 3000 race and there is money for both
// Then both succeed and the loser reports the balance after both debits (4000).
func TestWithdrawConcurrency_RetrySucceedsWithFreshBalance(t *testing.T) {
	store, codec, id := givenWalletWith10000(t)
	competitor := withdrawmoney.NewCommandHandler(store, codec)
	racy := &interleavingStore{Store: store, before: func() {
		if _, err := competitor.Handle(context.Background(), withdrawCmd(id, 3000, "")); err != nil {
			t.Errorf("competing withdrawal: %v", err)
		}
	}}

	reported, err := withdrawmoney.NewCommandHandler(racy, codec).Handle(context.Background(), withdrawCmd(id, 3000, ""))

	agg, view := storedBalances(t, store, codec, id)
	if err != nil {
		t.Fatalf("second 3000 withdrawal from 10000: err = %v, want success after retry", err)
	}
	if agg != 4000 {
		t.Errorf("stored balance = %d, want 4000", agg)
	}
	if reported != agg {
		t.Errorf("withdrawal reported balance %d, but stored balance is %d", reported, agg)
	}
	if view != agg {
		t.Errorf("projected balance %d != stored balance %d", view, agg)
	}
}

// maxWithdrawAttempts is the cap this spec fixes: one attempt plus two retries.
const maxWithdrawAttempts = 3

// contendedStore lands a competing 100-cent deposit (written straight to the
// underlying store, so it never re-enters this wrapper) before each of the
// first `conflicts` Appends, forcing each of those Appends to conflict. After
// that it stops interfering, so even a handler with no cap at all terminates
// instead of looping forever. It counts Loads and Appends to observe attempts.
// No locks are held across calls, so it cannot deadlock.
type contendedStore struct {
	eventstore.Store
	deposit   func()
	conflicts int

	mu      sync.Mutex
	loads   int
	appends int
}

func (s *contendedStore) Load(ctx context.Context, st eventstore.StreamID) ([]eventstore.Recorded, error) {
	s.mu.Lock()
	s.loads++
	s.mu.Unlock()
	return s.Store.Load(ctx, st)
}

func (s *contendedStore) Append(ctx context.Context, st eventstore.StreamID, ev []eventstore.Event, exp int) error {
	s.mu.Lock()
	s.appends++
	interfere := s.appends <= s.conflicts
	s.mu.Unlock()
	if interfere {
		s.deposit()
	}
	return s.Store.Append(ctx, st, ev, exp)
}

func (s *contendedStore) counts() (loads, appends int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads, s.appends
}

func newContendedStore(t *testing.T, store eventstore.Store, codec *eventstore.Codec, id string, conflicts int) *contendedStore {
	t.Helper()
	dep := depositmoney.NewCommandHandler(store, codec)
	return &contendedStore{Store: store, conflicts: conflicts, deposit: func() {
		if _, err := dep.Handle(context.Background(), depositmoney.Command{WalletID: id, UserID: "u",
			Amount: 100, Currency: "EUR", Reason: "r", Category: "Salary"}); err != nil {
			t.Errorf("competing deposit: %v", err)
		}
	}}
}

// Given a wallet with 10000
// When the first two attempts of a 1000 withdrawal both lose a race to a
// 100 deposit, and the third does not
// Then the third attempt succeeds, reloading before each attempt, and reports
// the stored balance (10000 + 200 - 1000 = 9200).
func TestWithdrawConcurrency_RecoversWithinCap(t *testing.T) {
	store, codec, id := givenWalletWith10000(t)
	contended := newContendedStore(t, store, codec, id, maxWithdrawAttempts-1)

	reported, err := withdrawmoney.NewCommandHandler(contended, codec).Handle(context.Background(), withdrawCmd(id, 1000, ""))

	agg, view := storedBalances(t, store, codec, id)
	loads, appends := contended.counts()
	if err != nil {
		t.Fatalf("withdrawal with %d conflicts: err = %v, want success on attempt %d", maxWithdrawAttempts-1, err, maxWithdrawAttempts)
	}
	if appends != maxWithdrawAttempts || loads != maxWithdrawAttempts {
		t.Errorf("loads = %d, appends = %d; want %d of each (reload before every attempt)", loads, appends, maxWithdrawAttempts)
	}
	if agg != 9200 {
		t.Errorf("stored balance = %d, want 9200", agg)
	}
	if reported != agg {
		t.Errorf("withdrawal reported balance %d, but stored balance is %d", reported, agg)
	}
	if view != agg {
		t.Errorf("projected balance %d != stored balance %d", view, agg)
	}
}

// Given a wallet with 10000
// When every attempt of a 1000 withdrawal loses a race (more conflicts than the cap)
// Then the handler gives up after exactly 3 attempts with an error wrapping
// eventstore.ErrConcurrencyConflict, and nothing is debited
// (10000 + 3 competing deposits of 100 = 10300).
func TestWithdrawConcurrency_CapExhausted(t *testing.T) {
	store, codec, id := givenWalletWith10000(t)
	contended := newContendedStore(t, store, codec, id, 10)

	_, err := withdrawmoney.NewCommandHandler(contended, codec).Handle(context.Background(), withdrawCmd(id, 1000, ""))

	agg, view := storedBalances(t, store, codec, id)
	loads, appends := contended.counts()
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Errorf("withdrawal on a permanently contended wallet: err = %v, want eventstore.ErrConcurrencyConflict", err)
	}
	if appends != maxWithdrawAttempts || loads != maxWithdrawAttempts {
		t.Errorf("loads = %d, appends = %d; want exactly %d of each before giving up", loads, appends, maxWithdrawAttempts)
	}
	if want := 10000 + 100*appends; agg != want {
		t.Errorf("stored balance = %d, want %d (competing deposits only, no withdrawal)", agg, want)
	}
	if view != agg {
		t.Errorf("projected balance %d != stored balance %d", view, agg)
	}
}

// Given a wallet with 10000
// When POST /wallets/:id/withdraw runs out of attempts on a contended wallet
// Then the API answers 409 {"error_code":"concurrency_conflict"}, not 500.
func TestWithdrawConcurrency_CapExhaustedHTTP409(t *testing.T) {
	store, codec, id := givenWalletWith10000(t)
	contended := newContendedStore(t, store, codec, id, 10)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/wallets/:id/withdraw", withdrawmoney.NewHandler(withdrawmoney.NewCommandHandler(contended, codec)).WithdrawMoney)
	body := `{"amount":1000,"currency":"EUR","reason":"r","category":"Other"}`
	req := httptest.NewRequest(http.MethodPost, "/wallets/"+id+"/withdraw", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middlewareUser(req.Context()))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "concurrency_conflict" {
		t.Errorf("error_code = %q, want concurrency_conflict; body = %s", resp["error_code"], w.Body.String())
	}
	if agg, _ := storedBalances(t, store, codec, id); agg != 10000+100*maxWithdrawAttempts {
		t.Errorf("stored balance = %d, want %d (no withdrawal)", agg, 10000+100*maxWithdrawAttempts)
	}
}

func middlewareUser(ctx context.Context) context.Context {
	return middleware.WithUserID(ctx, "u")
}
