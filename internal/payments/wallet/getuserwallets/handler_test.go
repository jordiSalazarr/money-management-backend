package getuserwallets_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
	"github.com/jordisalazar/money-management/internal/payments/wallet/getuserwallets"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
)

func setupRouter() (*gin.Engine, *projections.WalletsProjector) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			middleware.WithUserID(c.Request.Context(), "user-1"),
		)
		c.Next()
	})
	store := projections.NewMemoryWalletStore()
	projector := projections.NewWalletsProjector(store)
	getuserwallets.RegisterQueries(r, store)
	return r, projector
}

func TestGetUserWallets_Empty(t *testing.T) {
	router, _ := setupRouter()

	req := httptest.NewRequest(http.MethodGet, "/wallets", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	wallets := resp["wallets"].([]any)
	if len(wallets) != 0 {
		t.Errorf("wallets count = %d, want 0", len(wallets))
	}
}

func TestGetUserWallets_WithWallets(t *testing.T) {
	router, projector := setupRouter()

	// Feed the projector directly with wallet events
	projector.Handle(walletAddedEvent("wallet-1", "user-1", "Savings", "My savings", 50000, "EUR", "Savings"), eventstore.EventMetadata{})
	projector.Handle(walletAddedEvent("wallet-2", "user-1", "Cash", "", 10000, "EUR", "Cash"), eventstore.EventMetadata{})
	projector.Handle(walletAddedEvent("wallet-3", "user-2", "Other user", "", 5000, "USD", "Investment"), eventstore.EventMetadata{})

	req := httptest.NewRequest(http.MethodGet, "/wallets", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	wallets := resp["wallets"].([]any)
	if len(wallets) != 2 {
		t.Errorf("wallets count = %d, want 2", len(wallets))
	}
}
