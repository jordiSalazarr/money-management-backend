package disablewallet_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
	"github.com/jordisalazar/money-management/internal/payments/wallet/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/disablewallet"
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
	disablewallet.RegisterCommands(r, store)
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

func TestDisableWallet_Success(t *testing.T) {
	router := setupRouter()
	walletID := createWallet(t, router)

	body := map[string]any{
		"reason": "No longer needed",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/wallets/"+walletID+"/disable", bytes.NewReader(jsonBody))
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
}

func TestDisableWallet_AlreadyDisabled(t *testing.T) {
	router := setupRouter()
	walletID := createWallet(t, router)

	// Disable once
	body := map[string]any{"reason": "first disable"}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/wallets/"+walletID+"/disable", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first disable: status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Disable again
	body = map[string]any{"reason": "second disable"}
	jsonBody, _ = json.Marshal(body)
	req = httptest.NewRequest(http.MethodPost, "/wallets/"+walletID+"/disable", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusConflict, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "wallet_already_disabled" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "wallet_already_disabled")
	}
}

func TestDisableWallet_WalletNotFound(t *testing.T) {
	router := setupRouter()

	body := map[string]any{"reason": "cleanup"}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/wallets/nonexistent/disable", bytes.NewReader(jsonBody))
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
