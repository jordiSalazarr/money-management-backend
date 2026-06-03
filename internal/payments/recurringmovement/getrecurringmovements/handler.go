package getrecurringmovements

import (
	"log/slog"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
)

type Handler struct {
	store projections.RecurringMovementStore
}

func NewHandler(store projections.RecurringMovementStore) *Handler {
	return &Handler{store: store}
}

// Response contains the list of recurring movements for a wallet.
type Response struct {
	RecurringMovements []projections.RecurringMovementView `json:"recurring_movements"`
}

// GetRecurringMovements returns all recurring movements for a given wallet.
// @Summary      List wallet recurring movements
// @Description  Returns all recurring movements (active and inactive) for a wallet, ordered by created_at descending.
// @Tags         RecurringMovements
// @Produce      json
// @Param        id  path      string  true  "Wallet ID"
// @Success      200 {object}  Response
// @Failure      401 {object}  object  "unauthorized"
// @Security     BearerAuth
// @Router       /wallets/{id}/recurring-movements [get]
func (h *Handler) GetRecurringMovements(c *gin.Context) {
	walletID := c.Param("id")

	items, err := h.store.List(c.Request.Context(), projections.RecurringMovementFilter{
		WalletID: walletID,
	})
	if err != nil {
		slog.Error("failed to list recurring movements", "wallet_id", walletID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_code": "internal_error", "message": "failed to list recurring movements"})
		return
	}
	if items == nil {
		items = []projections.RecurringMovementView{}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	c.JSON(http.StatusOK, Response{RecurringMovements: items})
}
