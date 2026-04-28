package transfermoney

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/middleware"
	"github.com/jordisalazar/money-management/wallet"
	walletservices "github.com/jordisalazar/money-management/wallet/services"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

// Request is the payload for transferring money between wallets.
type Request struct {
	FromWalletID string `json:"from_wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	ToWalletID   string `json:"to_wallet_id" example:"660e8400-e29b-41d4-a716-446655440000"`
	Amount       int    `json:"amount" example:"3000"`
	Currency     string `json:"currency" example:"EUR"`
	Reason       string `json:"reason" example:"Rent split"`
}

// Response is the result of a transfer.
type Response struct {
	TransactionID string `json:"transaction_id" example:"770e8400-e29b-41d4-a716-446655440000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"same_wallet"`
	Message   string `json:"message" example:"source and destination wallets must differ"`
}

// TransferMoney moves funds between two wallets atomically.
// @Summary      Transfer money
// @Description  Transfers money from one wallet to another atomically. Both wallets must exist, share the same currency, and the source must have sufficient funds. Amount is in integer cents.
// @Tags         Transfers
// @Accept       json
// @Produce      json
// @Param        body  body      Request  true  "Transfer payload"
// @Success      201   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error (invalid_amount, same_wallet, currency_mismatch, insufficient_funds)"
// @Failure      401   {object}  ErrorResponse  "unauthorized"
// @Failure      404   {object}  ErrorResponse  "wallet_not_found"
// @Failure      409   {object}  ErrorResponse  "concurrency_conflict"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /transfers [post]
func (h *Handler) TransferMoney(c *gin.Context) {
	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	cmd := walletservices.TransferMoneyCommand{
		UserID:       middleware.GetUserID(c.Request.Context()),
		FromWalletID: req.FromWalletID,
		ToWalletID:   req.ToWalletID,
		Amount:       req.Amount,
		Currency:     req.Currency,
		Reason:       req.Reason,
	}

	transactionID, err := h.cmdHandler.Handle(c.Request.Context(), cmd)
	if err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusCreated, Response{TransactionID: transactionID})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, wallet.ErrMissingUserID):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_user_id", Message: err.Error()}
	case errors.Is(err, wallet.ErrInvalidAmount):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_amount", Message: err.Error()}
	case errors.Is(err, ErrSameWallet):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "same_wallet", Message: err.Error()}
	case errors.Is(err, wallet.ErrCurrencyMismatch):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "currency_mismatch", Message: err.Error()}
	case errors.Is(err, wallet.ErrInsufficientFunds):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "insufficient_funds", Message: err.Error()}
	case errors.Is(err, eventstore.ErrAggregateNotFound):
		return http.StatusNotFound, ErrorResponse{ErrorCode: "wallet_not_found", Message: "wallet not found"}
	case errors.Is(err, eventstore.ErrConcurrencyConflict):
		return http.StatusConflict, ErrorResponse{ErrorCode: "concurrency_conflict", Message: "please retry"}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
