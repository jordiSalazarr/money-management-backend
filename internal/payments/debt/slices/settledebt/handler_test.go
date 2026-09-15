package settledebt_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/settledebt"
	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

func setupRouter() (*gin.Engine, eventstore.Store, *eventstore.Codec) {
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
	settledebt.RegisterCommands(r, store, codec)
	return r, store, codec
}

func seedDebt(t *testing.T, store eventstore.Store, codec *eventstore.Codec, debtID string) {
	t.Helper()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	evt := debt.DebtRecorded{
		DebtID:    debtID,
		OwnerID:   "user-1",
		WalletID:  "wallet-1",
		Amount:    1000,
		Currency:  "EUR",
		Direction: "IN",
		Reason:    "Dinner",
		CreatedAt: now,
	}
	err := eventstore.AppendChanges(context.Background(), store, codec,
		streams.Debt(debtID), eventstore.NoStream,
		[]debt.DomainEvent{evt}, "corr-test", now)
	if err != nil {
		t.Fatalf("failed to seed debt: %v", err)
	}
}

func TestSettleDebt_Success(t *testing.T) {
	router, store, codec := setupRouter()
	seedDebt(t, store, codec, "debt-1")

	body := map[string]any{"settled_by": "user-1"}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/debts/debt-1/settle", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp settledebt.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.DebtID != "debt-1" {
		t.Errorf("debt_id = %q, want %q", resp.DebtID, "debt-1")
	}
	if resp.Status != "settled" {
		t.Errorf("status = %q, want %q", resp.Status, "settled")
	}
}

// A second request while the first is still in flight is a conflict, not
// "already settled": the money has not moved yet.
func TestSettleDebt_SettlementAlreadyInProgress(t *testing.T) {
	router, store, codec := setupRouter()
	seedDebt(t, store, codec, "debt-1")

	body := map[string]any{"settled_by": "user-1"}
	jsonBody, _ := json.Marshal(body)

	// Settle once
	req := httptest.NewRequest(http.MethodPost, "/debts/debt-1/settle", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first settle: status = %d, want %d", w.Code, http.StatusOK)
	}

	// Settle again
	jsonBody, _ = json.Marshal(body)
	req = httptest.NewRequest(http.MethodPost, "/debts/debt-1/settle", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("second settle: status = %d, want %d; body = %s", w.Code, http.StatusConflict, w.Body.String())
	}

	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "settlement_in_progress" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "settlement_in_progress")
	}
}

func TestSettleDebt_NotFound(t *testing.T) {
	router, _, _ := setupRouter()

	body := map[string]any{"settled_by": "user-1"}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/debts/nonexistent/settle", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "debt_not_found" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "debt_not_found")
	}
}

func TestSettleDebt_MissingSettledBy(t *testing.T) {
	router, store, codec := setupRouter()
	seedDebt(t, store, codec, "debt-1")

	body := map[string]any{"settled_by": ""}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/debts/debt-1/settle", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "missing_settled_by" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "missing_settled_by")
	}
}
