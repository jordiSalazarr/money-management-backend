package recorddebt_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/recorddebt"
	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
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
	recorddebt.RegisterCommands(r, store, codec)
	return r
}

func TestRecordDebt_Success(t *testing.T) {
	router := setupRouter()

	body := map[string]any{
		"wallet_id": "wallet-1",
		"amount":    1000,
		"currency":  "EUR",
		"direction": "IN",
		"reason":    "Dinner split",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/debts", bytes.NewReader(jsonBody))
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
	if resp["debt_id"] == "" {
		t.Error("expected debt_id in response")
	}
}

func TestRecordDebt_ValidationErrors(t *testing.T) {
	router := setupRouter()

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing wallet_id",
			body:       map[string]any{"amount": 1000, "currency": "EUR", "direction": "IN", "reason": "test"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "missing_wallet_id",
		},
		{
			name:       "zero amount",
			body:       map[string]any{"wallet_id": "w-1", "amount": 0, "currency": "EUR", "direction": "IN", "reason": "test"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_amount",
		},
		{
			name:       "negative amount",
			body:       map[string]any{"wallet_id": "w-1", "amount": -1, "currency": "EUR", "direction": "IN", "reason": "test"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_amount",
		},
		{
			name:       "amount too high",
			body:       map[string]any{"wallet_id": "w-1", "amount": 10_000_001, "currency": "EUR", "direction": "IN", "reason": "test"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_amount",
		},
		{
			name:       "invalid currency",
			body:       map[string]any{"wallet_id": "w-1", "amount": 1000, "currency": "GBP", "direction": "IN", "reason": "test"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_currency",
		},
		{
			name:       "invalid direction",
			body:       map[string]any{"wallet_id": "w-1", "amount": 1000, "currency": "EUR", "direction": "BOTH", "reason": "test"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_direction",
		},
		{
			name:       "missing reason",
			body:       map[string]any{"wallet_id": "w-1", "amount": 1000, "currency": "EUR", "direction": "IN", "reason": ""},
			wantStatus: http.StatusBadRequest,
			wantCode:   "missing_reason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/debts", bytes.NewReader(jsonBody))
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
