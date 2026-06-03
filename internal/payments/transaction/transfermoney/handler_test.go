package transfermoney_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
	"github.com/jordisalazar/money-management/internal/payments/transaction/transfermoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/addwallet"
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
	transfermoney.RegisterCommands(r, store)
	return r
}

func createWallet(t *testing.T, router *gin.Engine, name string, initialAmount int, currency, category string) string {
	t.Helper()
	body := map[string]any{
		"name":           name,
		"initial_amount": initialAmount,
		"currency":       currency,
		"category":       category,
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

func TestTransferMoney_Success(t *testing.T) {
	router := setupRouter()
	fromID := createWallet(t, router, "Savings", 10000, "EUR", "Savings")
	toID := createWallet(t, router, "Cash", 5000, "EUR", "Cash")

	body := map[string]any{
		"from_wallet_id": fromID,
		"to_wallet_id":   toID,
		"amount":         3000,
		"currency":       "EUR",
		"reason":         "Savings to cash",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["transaction_id"] == "" {
		t.Error("expected transaction_id in response")
	}
}

func TestTransferMoney_ValidationErrors(t *testing.T) {
	router := setupRouter()
	fromID := createWallet(t, router, "Savings", 10000, "EUR", "Savings")
	toID := createWallet(t, router, "Cash", 5000, "EUR", "Cash")
	usdID := createWallet(t, router, "USD Wallet", 5000, "USD", "Cash")

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "zero amount",
			body:       map[string]any{"from_wallet_id": fromID, "to_wallet_id": toID, "amount": 0, "currency": "EUR"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_amount",
		},
		{
			name:       "negative amount",
			body:       map[string]any{"from_wallet_id": fromID, "to_wallet_id": toID, "amount": -100, "currency": "EUR"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_amount",
		},
		{
			name:       "same wallet",
			body:       map[string]any{"from_wallet_id": fromID, "to_wallet_id": fromID, "amount": 1000, "currency": "EUR"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "same_wallet",
		},
		{
			name:       "insufficient funds",
			body:       map[string]any{"from_wallet_id": fromID, "to_wallet_id": toID, "amount": 10001, "currency": "EUR"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "insufficient_funds",
		},
		{
			name:       "currency mismatch on source",
			body:       map[string]any{"from_wallet_id": fromID, "to_wallet_id": toID, "amount": 1000, "currency": "USD"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "currency_mismatch",
		},
		{
			name:       "currency mismatch on destination",
			body:       map[string]any{"from_wallet_id": fromID, "to_wallet_id": usdID, "amount": 1000, "currency": "EUR"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "currency_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(jsonBody))
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

func TestTransferMoney_WalletNotFound(t *testing.T) {
	router := setupRouter()
	toID := createWallet(t, router, "Cash", 5000, "EUR", "Cash")

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "source wallet not found",
			body: map[string]any{"from_wallet_id": "nonexistent", "to_wallet_id": toID, "amount": 1000, "currency": "EUR"},
		},
		{
			name: "destination wallet not found",
			body: map[string]any{"from_wallet_id": toID, "to_wallet_id": "nonexistent", "amount": 1000, "currency": "EUR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(jsonBody))
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
		})
	}
}
