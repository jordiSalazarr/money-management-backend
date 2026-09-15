package getwalletmovements_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/getwalletmovements"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

func setupRouter() (*gin.Engine, *projections.MovementsProjector) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			middleware.WithUserID(c.Request.Context(), "user-1"),
		)
		c.Next()
	})
	store := projections.NewMemoryMovementStore()
	projector := projections.NewMovementsProjector(store)
	getwalletmovements.RegisterQueries(r, store)
	return r, projector
}

func TestGetWalletMovements_Empty(t *testing.T) {
	router, _ := setupRouter()

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/movements", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	movements := resp["movements"].([]any)
	if len(movements) != 0 {
		t.Errorf("movements count = %d, want 0", len(movements))
	}
}

func TestGetWalletMovements_Deposit(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC)

	projector.Handle(wallet.MoneyDeposited{
		WalletID:    "wallet-1",
		UserID:      "user-1",
		Amount:      5000,
		Currency:    "EUR",
		Reason:      "Birthday gift",
		DepositedAt: now,
	}, eventstore.EventMetadata{EventID: "evt-deposit-1"})

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/movements", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["movements"]) != 1 {
		t.Fatalf("movements count = %d, want 1", len(resp["movements"]))
	}
	m := resp["movements"][0]
	if m["type"] != "deposit" {
		t.Errorf("type = %q, want %q", m["type"], "deposit")
	}
	if int(m["amount"].(float64)) != 5000 {
		t.Errorf("amount = %v, want %d", m["amount"], 5000)
	}
}

func TestGetWalletMovements_Withdrawal(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 15, 0, 0, 0, time.UTC)

	projector.Handle(wallet.MoneyWithdrawn{
		WalletID:    "wallet-1",
		UserID:      "user-1",
		Amount:      3000,
		Currency:    "EUR",
		Reason:      "Lunch",
		Category:    "Food",
		WithdrawnAt: now,
	}, eventstore.EventMetadata{EventID: "evt-withdrawal-1"})

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/movements", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	var resp map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["movements"]) != 1 {
		t.Fatalf("movements count = %d, want 1", len(resp["movements"]))
	}
	m := resp["movements"][0]
	if m["type"] != "withdrawal" {
		t.Errorf("type = %q, want %q", m["type"], "withdrawal")
	}
	if m["category"] != "Food" {
		t.Errorf("category = %q, want %q", m["category"], "Food")
	}
}

// TestOneTransferYieldsExactlyTwoRows is the guard for the regression §4.8 of
// the migration plan warned about.
//
// The movements projector used to derive transfer_out and transfer_in rows
// from the transfer's completion event. Under the saga the two legs emit
// genuine MoneyWithdrawn and MoneyDeposited events, which the same projector
// already turns into rows — so keeping both would show every transfer twice.
// The completion event is now status-only and produces nothing here.
func TestOneTransferYieldsExactlyTwoRows(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 16, 0, 0, 0, time.UTC)

	// A completed transfer, as the wallet legs actually record it.
	projector.Handle(wallet.MoneyWithdrawn{
		WalletID: "wallet-a", UserID: "user-1", Amount: 1000, Currency: "EUR",
		Reason: "Savings to cash", Category: "Other", WithdrawnAt: now,
		IdempotencyKey: "tx-1:debit",
	}, eventstore.EventMetadata{EventID: "evt-debit"})
	projector.Handle(wallet.MoneyDeposited{
		WalletID: "wallet-b", UserID: "user-1", Amount: 1000, Currency: "EUR",
		Reason: "Savings to cash", Category: "Transfer In", DepositedAt: now,
		IdempotencyKey: "tx-1:credit",
	}, eventstore.EventMetadata{EventID: "evt-credit"})

	source := movementsFor(t, router, "wallet-a")
	if len(source) != 1 {
		t.Fatalf("source rows = %d, want exactly 1 — more than one means the transfer was counted twice", len(source))
	}
	if source[0]["type"] != "withdrawal" {
		t.Errorf("source type = %q, want withdrawal", source[0]["type"])
	}

	dest := movementsFor(t, router, "wallet-b")
	if len(dest) != 1 {
		t.Fatalf("destination rows = %d, want exactly 1", len(dest))
	}
	if dest[0]["type"] != "deposit" {
		t.Errorf("destination type = %q, want deposit", dest[0]["type"])
	}
}

// TestAFailedTransferShowsAsAWithdrawalAndARefund covers the other half of
// keeping this view a pure ledger: a transfer that failed after its debit made
// two real balance changes that net to zero, and both must appear or the
// running total stops matching the wallet.
func TestAFailedTransferShowsAsAWithdrawalAndARefund(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 16, 0, 0, 0, time.UTC)

	projector.Handle(wallet.MoneyWithdrawn{
		WalletID: "wallet-a", UserID: "user-1", Amount: 1000, Currency: "EUR",
		Reason: "Rent", Category: "Other", WithdrawnAt: now,
		IdempotencyKey: "tx-2:debit",
	}, eventstore.EventMetadata{EventID: "evt-debit-2"})
	projector.Handle(wallet.MoneyRefunded{
		WalletID: "wallet-a", UserID: "user-1", Amount: 1000, Currency: "EUR",
		Reason: "transfer tx-2 could not be credited", RefundedAt: now,
		IdempotencyKey: "tx-2:refund",
	}, eventstore.EventMetadata{EventID: "evt-refund-2"})

	rows := movementsFor(t, router, "wallet-a")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (the withdrawal and the refund)", len(rows))
	}

	seen := map[string]bool{}
	net := 0.0
	for _, r := range rows {
		seen[r["type"].(string)] = true
		amount := r["amount"].(float64)
		if r["type"] == "withdrawal" {
			net -= amount
		} else {
			net += amount
		}
	}
	if !seen["withdrawal"] || !seen["refund"] {
		t.Errorf("want both a withdrawal and a refund row, got %v", seen)
	}
	if net != 0 {
		t.Errorf("the two rows net to %v, want 0 — the wallet is whole", net)
	}
}

func movementsFor(t *testing.T, router *gin.Engine, walletID string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/wallets/"+walletID+"/movements", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp map[string][]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding movements for %s: %v", walletID, err)
	}
	return resp["movements"]
}

func TestGetWalletMovements_IsolatedByWallet(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC)

	projector.Handle(wallet.MoneyDeposited{WalletID: "wallet-1", UserID: "user-1", Amount: 5000, Currency: "EUR", DepositedAt: now}, eventstore.EventMetadata{EventID: "evt-dep-1"})
	projector.Handle(wallet.MoneyDeposited{WalletID: "wallet-2", UserID: "user-1", Amount: 3000, Currency: "EUR", DepositedAt: now}, eventstore.EventMetadata{EventID: "evt-dep-2"})

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/movements", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["movements"]) != 1 {
		t.Errorf("wallet-1 movements count = %d, want 1", len(resp["movements"]))
	}
}
