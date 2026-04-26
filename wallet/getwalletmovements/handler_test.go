package getwalletmovements_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/shared/middleware"
	"github.com/jordisalazar/money-management/transaction"
	"github.com/jordisalazar/money-management/wallet"
	"github.com/jordisalazar/money-management/wallet/getwalletmovements"
)

func setupRouter() (*gin.Engine, *getwalletmovements.MovementsProjector) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			middleware.WithUserID(c.Request.Context(), "user-1"),
		)
		c.Next()
	})
	projector := getwalletmovements.NewMovementsProjector()
	getwalletmovements.RegisterQueries(r, projector)
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
	})

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
	})

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

func TestGetWalletMovements_Transfer(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 16, 0, 0, 0, time.UTC)

	projector.Handle(transaction.TransactionCompleted{
		TransactionID: "txn-1",
		UserID:        "user-1",
		FromWalletID:  "wallet-a",
		ToWalletID:    "wallet-b",
		Amount:        1000,
		Currency:      "EUR",
		Reason:        "Savings to cash",
		CompletedAt:   now,
	})

	// Check source wallet — should have transfer_out
	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-a/movements", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var respA map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &respA)
	if len(respA["movements"]) != 1 {
		t.Fatalf("wallet-a movements count = %d, want 1", len(respA["movements"]))
	}
	if respA["movements"][0]["type"] != "transfer_out" {
		t.Errorf("wallet-a type = %q, want %q", respA["movements"][0]["type"], "transfer_out")
	}

	// Check destination wallet — should have transfer_in
	req = httptest.NewRequest(http.MethodGet, "/wallets/wallet-b/movements", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var respB map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &respB)
	if len(respB["movements"]) != 1 {
		t.Fatalf("wallet-b movements count = %d, want 1", len(respB["movements"]))
	}
	if respB["movements"][0]["type"] != "transfer_in" {
		t.Errorf("wallet-b type = %q, want %q", respB["movements"][0]["type"], "transfer_in")
	}
}

func TestGetWalletMovements_IsolatedByWallet(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC)

	projector.Handle(wallet.MoneyDeposited{WalletID: "wallet-1", UserID: "user-1", Amount: 5000, Currency: "EUR", DepositedAt: now})
	projector.Handle(wallet.MoneyDeposited{WalletID: "wallet-2", UserID: "user-1", Amount: 3000, Currency: "EUR", DepositedAt: now})

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/movements", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["movements"]) != 1 {
		t.Errorf("wallet-1 movements count = %d, want 1", len(resp["movements"]))
	}
}
