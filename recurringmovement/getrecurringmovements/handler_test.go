package getrecurringmovements_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/recurringmovement"
	"github.com/jordisalazar/money-management/recurringmovement/getrecurringmovements"
	"github.com/jordisalazar/money-management/recurringmovement/projections"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/middleware"
)

func setupRouter() (*gin.Engine, *projections.RecurringMovementsProjector) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			middleware.WithUserID(c.Request.Context(), "user-1"),
		)
		c.Next()
	})
	store := projections.NewMemoryRecurringMovementStore()
	projector := projections.NewRecurringMovementsProjector(store)
	getrecurringmovements.RegisterQueries(r, store)
	return r, projector
}

func TestGetRecurringMovements_Empty(t *testing.T) {
	router, _ := setupRouter()

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/recurring-movements", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	items := resp["recurring_movements"].([]any)
	if len(items) != 0 {
		t.Errorf("recurring_movements count = %d, want 0", len(items))
	}
}

func TestGetRecurringMovements_ReturnsMovementsForWallet(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	projector.Handle(recurringmovement.RecurringMovementRegistered{
		RecurringMovementID: "rm-1",
		WalletID:            "wallet-1",
		UserID:              "user-1",
		Type:                "deposit",
		Amount:              150000,
		Currency:            "EUR",
		Frequency:           "MONTHLY",
		Reason:              "Salary",
		StartDate:           start,
		RegisteredAt:        now,
	}, eventstore.EventMetadata{})

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/recurring-movements", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["recurring_movements"]) != 1 {
		t.Fatalf("recurring_movements count = %d, want 1", len(resp["recurring_movements"]))
	}
	m := resp["recurring_movements"][0]
	if m["type"] != "deposit" {
		t.Errorf("type = %q, want %q", m["type"], "deposit")
	}
	if int(m["amount"].(float64)) != 150000 {
		t.Errorf("amount = %v, want %d", m["amount"], 150000)
	}
	if m["frequency"] != "MONTHLY" {
		t.Errorf("frequency = %q, want %q", m["frequency"], "MONTHLY")
	}
	if m["reason"] != "Salary" {
		t.Errorf("reason = %q, want %q", m["reason"], "Salary")
	}
	if m["active"] != true {
		t.Errorf("active = %v, want true", m["active"])
	}
}

func TestGetRecurringMovements_IsolatedByWallet(t *testing.T) {
	router, projector := setupRouter()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	projector.Handle(recurringmovement.RecurringMovementRegistered{
		RecurringMovementID: "rm-1",
		WalletID:            "wallet-1",
		UserID:              "user-1",
		Type:                "deposit",
		Amount:              150000,
		Currency:            "EUR",
		Frequency:           "MONTHLY",
		Reason:              "Salary",
		StartDate:           start,
		RegisteredAt:        now,
	}, eventstore.EventMetadata{})
	projector.Handle(recurringmovement.RecurringMovementRegistered{
		RecurringMovementID: "rm-2",
		WalletID:            "wallet-2",
		UserID:              "user-1",
		Type:                "withdrawal",
		Amount:              5000,
		Currency:            "EUR",
		Frequency:           "DAILY",
		Reason:              "Coffee subscription",
		StartDate:           start,
		RegisteredAt:        now,
	}, eventstore.EventMetadata{})

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/recurring-movements", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	var resp map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["recurring_movements"]) != 1 {
		t.Errorf("wallet-1 recurring_movements count = %d, want 1", len(resp["recurring_movements"]))
	}
}

func TestGetRecurringMovements_SortedByCreatedAtDescending(t *testing.T) {
	router, projector := setupRouter()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	projector.Handle(recurringmovement.RecurringMovementRegistered{
		RecurringMovementID: "rm-older",
		WalletID:            "wallet-1",
		UserID:              "user-1",
		Type:                "deposit",
		Amount:              100,
		Currency:            "EUR",
		Frequency:           "MONTHLY",
		Reason:              "Older",
		StartDate:           start,
		RegisteredAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, eventstore.EventMetadata{})
	projector.Handle(recurringmovement.RecurringMovementRegistered{
		RecurringMovementID: "rm-newer",
		WalletID:            "wallet-1",
		UserID:              "user-1",
		Type:                "withdrawal",
		Amount:              200,
		Currency:            "EUR",
		Frequency:           "WEEKLY",
		Reason:              "Newer",
		StartDate:           start,
		RegisteredAt:        time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}, eventstore.EventMetadata{})

	req := httptest.NewRequest(http.MethodGet, "/wallets/wallet-1/recurring-movements", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	var resp map[string][]map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["recurring_movements"]) != 2 {
		t.Fatalf("count = %d, want 2", len(resp["recurring_movements"]))
	}
	if resp["recurring_movements"][0]["reason"] != "Newer" {
		t.Errorf("first item reason = %q, want %q", resp["recurring_movements"][0]["reason"], "Newer")
	}
	if resp["recurring_movements"][1]["reason"] != "Older" {
		t.Errorf("second item reason = %q, want %q", resp["recurring_movements"][1]["reason"], "Older")
	}
}
