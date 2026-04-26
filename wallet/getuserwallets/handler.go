package getuserwallets

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/shared/middleware"
	"github.com/jordisalazar/money-management/wallet/addwallet"
)

type Handler struct {
	projector *addwallet.WalletsProjector
}

func NewHandler(projector *addwallet.WalletsProjector) *Handler {
	return &Handler{projector: projector}
}

// Response contains the list of wallets for a user.
type Response struct {
	Wallets []addwallet.WalletView `json:"wallets"`
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

	wallets := h.projector.ListByUserID(userID)
	if wallets == nil {
		wallets = []addwallet.WalletView{}
	}

	c.JSON(http.StatusOK, Response{Wallets: wallets})
}
