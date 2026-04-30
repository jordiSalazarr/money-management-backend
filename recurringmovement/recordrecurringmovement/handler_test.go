package recordrecurringmovement_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/recurringmovement/recordrecurringmovement"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/middleware"
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
	recordrecurringmovement.RegisterCommands(r, store)
	return r
}

func TestRecordRecurringMovement_Success(t *testing.T) {
	router := setupRouter()

	body := map[string]any{
		"wallet_id":  "wallet-1",
		"type":       "deposit",
		"amount":     1000,
		"currency":   "EUR",
		"frequency":  "MONTHLY",
		"reason":     "Salary",
		"start_date": "2099-05-01",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/recurring-movements", bytes.NewReader(jsonBody))
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
	if resp["recurring_movement_id"] == "" {
		t.Error("expected recurring_movement_id in response")
	}
}

func TestRecordRecurringMovement_WithEndDate(t *testing.T) {
	router := setupRouter()

	body := map[string]any{
		"wallet_id":  "wallet-1",
		"type":       "withdrawal",
		"amount":     5000,
		"currency":   "USD",
		"frequency":  "WEEKLY",
		"reason":     "Savings",
		"start_date": "2099-05-01",
		"end_date":   "2099-12-31",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/recurring-movements", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}
}

func TestRecordRecurringMovement_ValidationErrors(t *testing.T) {
	router := setupRouter()

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing wallet_id",
			body:       map[string]any{"type": "deposit", "amount": 1000, "currency": "EUR", "frequency": "MONTHLY", "reason": "test", "start_date": "2099-05-01"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "missing_wallet_id",
		},
		{
			name:       "invalid type",
			body:       map[string]any{"wallet_id": "w-1", "type": "transfer", "amount": 1000, "currency": "EUR", "frequency": "MONTHLY", "reason": "test", "start_date": "2099-05-01"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_type",
		},
		{
			name:       "zero amount",
			body:       map[string]any{"wallet_id": "w-1", "type": "deposit", "amount": 0, "currency": "EUR", "frequency": "MONTHLY", "reason": "test", "start_date": "2099-05-01"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_amount",
		},
		{
			name:       "invalid currency",
			body:       map[string]any{"wallet_id": "w-1", "type": "deposit", "amount": 1000, "currency": "GBP", "frequency": "MONTHLY", "reason": "test", "start_date": "2099-05-01"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_currency",
		},
		{
			name:       "invalid frequency",
			body:       map[string]any{"wallet_id": "w-1", "type": "deposit", "amount": 1000, "currency": "EUR", "frequency": "YEARLY", "reason": "test", "start_date": "2099-05-01"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_frequency",
		},
		{
			name:       "missing reason",
			body:       map[string]any{"wallet_id": "w-1", "type": "deposit", "amount": 1000, "currency": "EUR", "frequency": "MONTHLY", "reason": "", "start_date": "2099-05-01"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "missing_reason",
		},
		{
			name:       "invalid start_date format",
			body:       map[string]any{"wallet_id": "w-1", "type": "deposit", "amount": 1000, "currency": "EUR", "frequency": "MONTHLY", "reason": "test", "start_date": "not-a-date"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_start_date",
		},
		{
			name:       "end_date before start_date",
			body:       map[string]any{"wallet_id": "w-1", "type": "deposit", "amount": 1000, "currency": "EUR", "frequency": "MONTHLY", "reason": "test", "start_date": "2099-05-01", "end_date": "2099-04-01"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_end_date",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/recurring-movements", bytes.NewReader(jsonBody))
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
