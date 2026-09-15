package getdebts

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/debt/projections"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

type Handler struct {
	store projections.DebtStore
}

func NewHandler(store projections.DebtStore) *Handler {
	return &Handler{store: store}
}

// Response contains the list of debts.
type Response struct {
	Debts []projections.DebtView `json:"debts"`
}

// GetDebts returns debts for the authenticated user with optional filters.
// @Summary      List debts
// @Description  Returns debts for the authenticated user. Filterable by wallet_id, direction, and status.
// @Tags         Debts
// @Produce      json
// @Param        wallet_id  query  string  false  "Filter by wallet ID"
// @Param        direction  query  string  false  "Filter by direction (IN or OUT)"
// @Param        status     query  string  false  "Filter by status (created)"
// @Success      200  {object}  Response
// @Failure      401  {object}  object  "unauthorized"
// @Security     BearerAuth
// @Router       /debts [get]
func (h *Handler) GetDebts(c *gin.Context) {
	ownerID := middleware.GetUserID(c.Request.Context())

	filter := projections.DebtFilter{
		OwnerID: ownerID,
	}

	if walletID := c.Query("wallet_id"); walletID != "" {
		filter.WalletID = walletID
	}
	if direction := c.Query("direction"); direction != "" {
		filter.Direction = &direction
	}
	if status := c.Query("status"); status != "" {
		filter.Status = &status
	}

	debts, err := h.store.List(c.Request.Context(), filter)
	if err != nil {
		slog.Error("failed to list debts", "owner_id", ownerID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_code": "internal_error", "message": "failed to list debts"})
		return
	}
	if debts == nil {
		debts = []projections.DebtView{}
	}

	c.JSON(http.StatusOK, Response{Debts: debts})
}
