package stoprecurringmovement_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/recurringmovement"
	"github.com/jordisalazar/money-management/recurringmovement/recordrecurringmovement"
	"github.com/jordisalazar/money-management/recurringmovement/stoprecurringmovement"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/middleware"
)

func setupRouter(userID string) (*gin.Engine, eventstore.Store) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			middleware.WithUserID(c.Request.Context(), userID),
		)
		c.Next()
	})
	store := eventstore.NewMemoryStore()
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementRegistered, func() any { return &recurringmovement.RecurringMovementRegistered{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementExecuted, func() any { return &recurringmovement.RecurringMovementExecuted{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementStopped, func() any { return &recurringmovement.RecurringMovementStopped{} })
	recordrecurringmovement.RegisterCommands(r, store)
	stoprecurringmovement.RegisterCommands(r, store)
	return r, store
}

func createRecurringMovement(t *testing.T, router *gin.Engine) string {
	t.Helper()
	body := map[string]any{
		"wallet_id":  "wallet-1",
		"type":       "deposit",
		"amount":     1000,
		"currency":   "EUR",
		"frequency":  "MONTHLY",
		"reason":     "Salary",
		"start_date": time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"),
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/recurring-movements", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("setup: create failed with status %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp["recurring_movement_id"]
}

func TestStopRecurringMovement_Success(t *testing.T) {
	router, _ := setupRouter("user-1")
	id := createRecurringMovement(t, router)

	body := map[string]any{"reason": "No longer needed"}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/recurring-movements/"+id+"/stop", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["recurring_movement_id"] != id {
		t.Errorf("recurring_movement_id = %q, want %q", resp["recurring_movement_id"], id)
	}
	if resp["stopped_at"] == "" {
		t.Error("expected stopped_at in response")
	}
}

func TestStopRecurringMovement_EmptyBody(t *testing.T) {
	router, _ := setupRouter("user-1")
	id := createRecurringMovement(t, router)

	req := httptest.NewRequest(http.MethodPost, "/recurring-movements/"+id+"/stop", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestStopRecurringMovement_NotFound(t *testing.T) {
	router, _ := setupRouter("user-1")

	req := httptest.NewRequest(http.MethodPost, "/recurring-movements/nonexistent-id/stop", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "recurring_movement_not_found" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "recurring_movement_not_found")
	}
}

func TestStopRecurringMovement_AlreadyStopped(t *testing.T) {
	router, _ := setupRouter("user-1")
	id := createRecurringMovement(t, router)

	// Stop once
	req := httptest.NewRequest(http.MethodPost, "/recurring-movements/"+id+"/stop", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first stop: status = %d, want %d", w.Code, http.StatusOK)
	}

	// Stop again
	req = httptest.NewRequest(http.MethodPost, "/recurring-movements/"+id+"/stop", nil)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "not_active" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "not_active")
	}
}

func TestStopRecurringMovement_NotOwner(t *testing.T) {
	// Create with user-1
	routerUser1, store := setupRouter("user-1")
	id := createRecurringMovement(t, routerUser1)

	// Try to stop with user-2
	gin.SetMode(gin.TestMode)
	routerUser2 := gin.New()
	routerUser2.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			middleware.WithUserID(c.Request.Context(), "user-2"),
		)
		c.Next()
	})
	stoprecurringmovement.RegisterCommands(routerUser2, store)

	req := httptest.NewRequest(http.MethodPost, "/recurring-movements/"+id+"/stop", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	routerUser2.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusForbidden, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "not_owner" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "not_owner")
	}
}
