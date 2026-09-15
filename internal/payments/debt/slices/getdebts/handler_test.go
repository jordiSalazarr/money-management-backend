package getdebts_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/getdebts"
	"github.com/jordisalazar/money-management/internal/payments/debt/projections"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

func setupRouter() (*gin.Engine, *projections.MemoryDebtStore) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			middleware.WithUserID(c.Request.Context(), "user-1"),
		)
		c.Next()
	})
	store := projections.NewMemoryDebtStore()
	getdebts.RegisterQueries(r, store)
	return r, store
}

func TestGetDebts_Empty(t *testing.T) {
	router, _ := setupRouter()

	req := httptest.NewRequest(http.MethodGet, "/debts", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp getdebts.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Debts) != 0 {
		t.Errorf("debts count = %d, want 0", len(resp.Debts))
	}
}

func TestGetDebts_WithData(t *testing.T) {
	router, store := setupRouter()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	_ = store.Upsert(context.Background(), &projections.DebtView{
		ID: "debt-1", OwnerID: "user-1", WalletID: "wallet-1",
		Amount: 1000, Currency: "EUR", Direction: "IN",
		Reason: "Dinner", Status: "created", CreatedAt: now,
	})
	_ = store.Upsert(context.Background(), &projections.DebtView{
		ID: "debt-2", OwnerID: "user-1", WalletID: "wallet-2",
		Amount: 5000, Currency: "USD", Direction: "OUT",
		Reason: "Tickets", Status: "created", CreatedAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/debts", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp getdebts.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Debts) != 2 {
		t.Errorf("debts count = %d, want 2", len(resp.Debts))
	}
}

func TestGetDebts_FilterByDirection(t *testing.T) {
	router, store := setupRouter()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	_ = store.Upsert(context.Background(), &projections.DebtView{
		ID: "debt-1", OwnerID: "user-1", Amount: 1000, Currency: "EUR",
		Direction: "IN", Reason: "Dinner", Status: "created", CreatedAt: now,
	})
	_ = store.Upsert(context.Background(), &projections.DebtView{
		ID: "debt-2", OwnerID: "user-1", Amount: 5000, Currency: "USD",
		Direction: "OUT", Reason: "Tickets", Status: "created", CreatedAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/debts?direction=IN", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	var resp getdebts.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Debts) != 1 {
		t.Errorf("debts count = %d, want 1", len(resp.Debts))
	}
	if resp.Debts[0].Direction != "IN" {
		t.Errorf("direction = %q, want %q", resp.Debts[0].Direction, "IN")
	}
}
