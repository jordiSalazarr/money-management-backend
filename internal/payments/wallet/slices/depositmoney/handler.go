package depositmoney

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

// Request is the payload for depositing money.
type Request struct {
	Amount   int    `json:"amount" example:"5000"`
	Currency string `json:"currency" example:"EUR"`
	Reason   string `json:"reason" example:"Salary"`
	Category string `json:"category" example:"Salary"`
}

// Response is the result of a deposit.
type Response struct {
	WalletID   string `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	NewBalance int    `json:"new_balance" example:"15000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"invalid_amount"`
	Message   string `json:"message" example:"amount must be positive"`
}

// DepositMoney adds funds to a wallet.
// @Summary      Deposit money
// @Description  Deposits money into an existing wallet. Amount is in integer cents.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Param        id    path      string   true  "Wallet ID"
// @Param        body  body      Request  true  "Deposit payload"
// @Success      200   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error (invalid_amount, currency_mismatch)"
// @Failure      401   {object}  ErrorResponse  "unauthorized"
// @Failure      404   {object}  ErrorResponse  "wallet_not_found"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /wallets/{id}/deposit [post]
func (h *Handler) DepositMoney(c *gin.Context) {
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
	case errors.Is(err, wallet.ErrInvalidDepositCategory):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_category", Message: err.Error()}
	case errors.Is(err, wallet.ErrWalletDisabled):
		return http.StatusConflict, ErrorResponse{ErrorCode: "wallet_disabled", Message: err.Error()}
	case errors.Is(err, eventstore.ErrAggregateNotFound):
		return http.StatusNotFound, ErrorResponse{ErrorCode: "wallet_not_found", Message: "wallet not found"}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
