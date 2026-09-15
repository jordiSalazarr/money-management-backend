package getwalletmovements

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
)

type Handler struct {
	store projections.MovementStore
}

func NewHandler(store projections.MovementStore) *Handler {
	return &Handler{store: store}
}

// Response contains the list of movements for a wallet.
type Response struct {
	Movements []projections.MovementView `json:"movements"`
}

// GetWalletMovements returns all movements for a given wallet.
// @Summary      List wallet movements
// @Description  Returns all movements (deposits, withdrawals, transfers in/out) for a wallet, ordered by occurrence.
// @Tags         Wallets
// @Produce      json
// @Param        id  path      string  true  "Wallet ID"
// @Success      200 {object}  Response
// @Failure      401 {object}  object  "unauthorized"
// @Security     BearerAuth
// @Router       /wallets/{id}/movements [get]
func (h *Handler) GetWalletMovements(c *gin.Context) {
	walletID := c.Param("id")

	movements, err := h.store.ListByWalletID(c.Request.Context(), walletID)
	if err != nil {
		slog.Error("failed to list movements", "wallet_id", walletID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_code": "internal_error", "message": "failed to list movements"})
		return
	}
	if movements == nil {
		movements = []projections.MovementView{}
	}

	c.JSON(http.StatusOK, Response{Movements: movements})
}
