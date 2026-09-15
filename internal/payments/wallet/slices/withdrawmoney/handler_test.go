package withdrawmoney_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			middleware.WithUserID(c.Request.Context(), "user-1"),
		)
		c.Next()
	})
	store := eventstore.NewMemoryStore()
	addwallet.RegisterCommands(r, store)
	withdrawmoney.RegisterCommands(r, store)
	return r
}

func createWallet(t *testing.T, router *gin.Engine) string {
	t.Helper()
	body := map[string]any{
		"name":           "Test Wallet",
		"initial_amount": 10000,
		"currency":       "EUR",
		"category":       "Cash",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/wallets", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create wallet: status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp["wallet_id"]
}

func TestWithdrawMoney_Success(t *testing.T) {
	router := setupRouter()
	walletID := createWallet(t, router)

	body := map[string]any{
		"amount":   3000,
		"currency": "EUR",
		"reason":   "Lunch",
		"category": "Food & Groceries",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/wallets/"+walletID+"/withdraw", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["wallet_id"] != walletID {
		t.Errorf("wallet_id = %q, want %q", resp["wallet_id"], walletID)
	}
	if int(resp["new_balance"].(float64)) != 7000 {
		t.Errorf("new_balance = %v, want %d", resp["new_balance"], 7000)
	}
}

func TestWithdrawMoney_ValidationErrors(t *testing.T) {
	router := setupRouter()
	walletID := createWallet(t, router)

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "zero amount",
			body:       map[string]any{"amount": 0, "currency": "EUR", "category": "Food & Groceries"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_amount",
		},
		{
			name:       "negative amount",
			body:       map[string]any{"amount": -100, "currency": "EUR", "category": "Food & Groceries"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_amount",
		},
		{
			name:       "currency mismatch",
			body:       map[string]any{"amount": 5000, "currency": "USD", "category": "Food & Groceries"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "currency_mismatch",
		},
		{
			name:       "invalid category",
			body:       map[string]any{"amount": 5000, "currency": "EUR", "category": "InvalidCat"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_category",
		},
		{
			name:       "insufficient funds",
			body:       map[string]any{"amount": 10001, "currency": "EUR", "category": "Food & Groceries"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "insufficient_funds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/wallets/"+walletID+"/withdraw", bytes.NewReader(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tt.wantStatus, w.Body.String())
			}

			var resp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}
			if resp["error_code"] != tt.wantCode {
				t.Errorf("error_code = %q, want %q", resp["error_code"], tt.wantCode)
			}
		})
	}
}

func TestWithdrawMoney_WalletNotFound(t *testing.T) {
	router := setupRouter()

	body := map[string]any{
		"amount":   5000,
		"currency": "EUR",
		"category": "Food & Groceries",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/wallets/nonexistent/withdraw", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "wallet_not_found" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "wallet_not_found")
	}
}
