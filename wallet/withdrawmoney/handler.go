package withdrawmoney

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/middleware"
	"github.com/jordisalazar/money-management/wallet"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

// Request is the payload for withdrawing money.
type Request struct {
	Amount   int    `json:"amount" example:"2000"`
	Currency string `json:"currency" example:"EUR"`
	Reason   string `json:"reason" example:"Groceries"`
	Category string `json:"category" example:"food"`
}

// Response is the result of a withdrawal.
type Response struct {
	WalletID   string `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	NewBalance int    `json:"new_balance" example:"8000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"insufficient_funds"`
	Message   string `json:"message" example:"cannot withdraw 2000: balance is 1500"`
}

// WithdrawMoney removes funds from a wallet.
// @Summary      Withdraw money
// @Description  Withdraws money from an existing wallet. Amount is in integer cents. Fails if balance would go negative.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Param        id    path      string   true  "Wallet ID"
// @Param        body  body      Request  true  "Withdrawal payload"
// @Success      200   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error (invalid_amount, currency_mismatch, invalid_category, insufficient_funds)"
// @Failure      401   {object}  ErrorResponse  "unauthorized"
// @Failure      404   {object}  ErrorResponse  "wallet_not_found"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /wallets/{id}/withdraw [post]
func (h *Handler) WithdrawMoney(c *gin.Context) {
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
		WalletID: walletID,
		UserID:   middleware.GetUserID(c.Request.Context()),
		Amount:   req.Amount,
		Currency: req.Currency,
		Reason:   req.Reason,
		Category: req.Category,
	}

	newBalance, err := h.cmdHandler.Handle(c.Request.Context(), cmd)
	if err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusOK, Response{
		WalletID:   walletID,
		NewBalance: newBalance,
	})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, wallet.ErrMissingUserID):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_user_id", Message: err.Error()}
	case errors.Is(err, wallet.ErrInvalidAmount):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_amount", Message: err.Error()}
	case errors.Is(err, wallet.ErrCurrencyMismatch):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "currency_mismatch", Message: err.Error()}
	case errors.Is(err, wallet.ErrInvalidWithdrawCategory):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_category", Message: err.Error()}
	case errors.Is(err, wallet.ErrInsufficientFunds):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "insufficient_funds", Message: err.Error()}
	case errors.Is(err, wallet.ErrWalletDisabled):
		return http.StatusConflict, ErrorResponse{ErrorCode: "wallet_disabled", Message: err.Error()}
	case errors.Is(err, eventstore.ErrAggregateNotFound):
		return http.StatusNotFound, ErrorResponse{ErrorCode: "wallet_not_found", Message: "wallet not found"}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
