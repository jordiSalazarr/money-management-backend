package getwalletmovements

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	projector *MovementsProjector
}

func NewHandler(projector *MovementsProjector) *Handler {
	return &Handler{projector: projector}
}

// Response contains the list of movements for a wallet.
type Response struct {
	Movements []MovementView `json:"movements"`
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

	movements := h.projector.ListByWalletID(walletID)
	if movements == nil {
		movements = []MovementView{}
	}

	c.JSON(http.StatusOK, Response{Movements: movements})
}
