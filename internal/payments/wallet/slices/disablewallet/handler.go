package disablewallet

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

// Request is the payload for disabling a wallet.
type Request struct {
	Reason string `json:"reason" example:"No longer needed"`
}

// Response is the result of disabling a wallet.
type Response struct {
	WalletID string `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"wallet_already_disabled"`
	Message   string `json:"message" example:"wallet is already disabled"`
}

// DisableWallet soft-deletes a wallet by marking it as disabled.
// @Summary      Disable wallet
// @Description  Disables a wallet, preventing any further deposits, withdrawals, or transfers. The wallet remains visible in queries.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Param        id    path      string   true  "Wallet ID"
// @Param        body  body      Request  true  "Disable payload"
// @Success      200   {object}  Response
// @Failure      404   {object}  ErrorResponse  "wallet_not_found"
// @Failure      409   {object}  ErrorResponse  "wallet_already_disabled"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /wallets/{id}/disable [post]
func (h *Handler) DisableWallet(c *gin.Context) {
	walletID := c.Param("id")

	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	cmd := Command{
		WalletID:   walletID,
		UserID:     middleware.GetUserID(c.Request.Context()),
		Reason:     req.Reason,
		DisabledAt: time.Now().UTC(),
	}

	err := h.cmdHandler.Handle(c.Request.Context(), cmd)
	if err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusOK, Response{
		WalletID: walletID,
	})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, wallet.ErrWalletAlreadyDisabled):
		return http.StatusConflict, ErrorResponse{ErrorCode: "wallet_already_disabled", Message: err.Error()}
	case errors.Is(err, eventstore.ErrAggregateNotFound):
		return http.StatusNotFound, ErrorResponse{ErrorCode: "wallet_not_found", Message: "wallet not found"}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
