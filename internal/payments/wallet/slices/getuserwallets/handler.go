package getuserwallets

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

type Handler struct {
	store projections.WalletStore
}

func NewHandler(store projections.WalletStore) *Handler {
	return &Handler{store: store}
}

// Response contains the list of wallets for a user.
type Response struct {
	Wallets []projections.WalletView `json:"wallets"`
}

// GetUserWallets returns all wallets for a given user.
// @Summary      List user wallets
// @Description  Returns all wallets belonging to the authenticated user, including current balances.
// @Tags         Wallets
// @Produce      json
// @Success      200  {object}  Response
// @Failure      401  {object}  object  "unauthorized"
// @Security     BearerAuth
// @Router       /wallets [get]
func (h *Handler) GetUserWallets(c *gin.Context) {
	userID := middleware.GetUserID(c.Request.Context())

	wallets, err := h.store.ListByUserID(c.Request.Context(), userID)
	if err != nil {
		slog.Error("failed to list wallets", "user_id", userID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_code": "internal_error", "message": "failed to list wallets"})
		return
	}
	if wallets == nil {
		wallets = []projections.WalletView{}
	}

	c.JSON(http.StatusOK, Response{Wallets: wallets})
}
