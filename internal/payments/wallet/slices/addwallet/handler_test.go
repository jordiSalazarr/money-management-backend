package addwallet_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
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
	codec := eventcatalog.NewCodec()
	addwallet.RegisterCommands(r, store, codec)
	return r
}

func TestAddWallet_Success(t *testing.T) {
	router := setupRouter()

	body := map[string]any{
		"name":           "My Savings",
		"description":    "Main savings account",
		"initial_amount": 50000,
		"currency":       "EUR",
		"category":       "Savings",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/wallets", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["wallet_id"] == "" {
		t.Error("expected wallet_id in response")
	}
}

func TestAddWallet_ValidationErrors(t *testing.T) {
	router := setupRouter()

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing name",
			body:       map[string]any{"initial_amount": 0, "currency": "EUR", "category": "Cash"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "missing_name",
		},
		{
			name:       "negative initial_amount",
			body:       map[string]any{"name": "Test", "initial_amount": -1, "currency": "EUR", "category": "Cash"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_initial_amount",
		},
		{
			name:       "initial_amount too high",
			body:       map[string]any{"name": "Test", "initial_amount": 10_000_001, "currency": "EUR", "category": "Cash"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_initial_amount",
		},
		{
			name:       "invalid currency",
			body:       map[string]any{"name": "Test", "initial_amount": 0, "currency": "GBP", "category": "Cash"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_currency",
		},
		{
			name:       "invalid category",
			body:       map[string]any{"name": "Test", "initial_amount": 0, "currency": "EUR", "category": "Stocks"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_category",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/wallets", bytes.NewReader(jsonBody))
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
