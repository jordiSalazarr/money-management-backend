package requesttransfer

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
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

// Request is the payload for requesting a transfer between wallets.
type Request struct {
	FromWalletID string `json:"from_wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	ToWalletID   string `json:"to_wallet_id" example:"660e8400-e29b-41d4-a716-446655440000"`
	Amount       int    `json:"amount" example:"3000"`
	Currency     string `json:"currency" example:"EUR"`
	Reason       string `json:"reason" example:"Rent split"`
}

// Response is the acknowledgement of a requested transfer. The transfer has
// not happened yet — poll GET /transfers/{id} for its outcome.
type Response struct {
	TransferID string `json:"transfer_id" example:"770e8400-e29b-41d4-a716-446655440000"`
	Status     string `json:"status" example:"requested"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"same_wallet"`
	Message   string `json:"message" example:"source and destination wallet must differ"`
}

// RequestTransfer starts a transfer between two wallets.
// @Summary      Request a transfer
// @Description  Requests a move of money between two wallets and returns immediately with 202. The transfer is carried out asynchronously in two steps and may still fail; poll GET /transfers/{id} for the outcome. A failed transfer moves no money. Amount is in integer cents.
// @Tags         Transfers
// @Accept       json
// @Produce      json
// @Param        body  body      Request  true  "Transfer payload"
// @Success      202   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error (invalid_amount, same_wallet, currency_mismatch, insufficient_funds)"
// @Failure      401   {object}  ErrorResponse  "unauthorized"
// @Failure      404   {object}  ErrorResponse  "wallet_not_found"
// @Failure      409   {object}  ErrorResponse  "wallet_disabled"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /transfers [post]
func (h *Handler) RequestTransfer(c *gin.Context) {
	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	transferID, err := h.cmdHandler.Handle(c.Request.Context(), Command{
		UserID:       middleware.GetUserID(c.Request.Context()),
		FromWalletID: req.FromWalletID,
		ToWalletID:   req.ToWalletID,
		Amount:       req.Amount,
		Currency:     req.Currency,
		Reason:       req.Reason,
	})
	if err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	// 202, not 201: the transfer has been accepted, not performed.
	c.JSON(http.StatusAccepted, Response{
		TransferID: transferID,
		Status:     transfer.StatusRequested,
	})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, transfer.ErrMissingUserID):
		return http.StatusUnauthorized, ErrorResponse{ErrorCode: "unauthorized", Message: err.Error()}
	case errors.Is(err, transfer.ErrInvalidAmount), errors.Is(err, wallet.ErrInvalidAmount):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_amount", Message: err.Error()}
	case errors.Is(err, transfer.ErrSameWallet):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "same_wallet", Message: err.Error()}
	case errors.Is(err, wallet.ErrCurrencyMismatch):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "currency_mismatch", Message: err.Error()}
	case errors.Is(err, wallet.ErrInsufficientFunds):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "insufficient_funds", Message: err.Error()}
	case errors.Is(err, wallet.ErrWalletDisabled):
		return http.StatusConflict, ErrorResponse{ErrorCode: "wallet_disabled", Message: err.Error()}
	case errors.Is(err, eventstore.ErrAggregateNotFound):
		return http.StatusNotFound, ErrorResponse{ErrorCode: "wallet_not_found", Message: err.Error()}
	case errors.Is(err, eventstore.ErrConcurrencyConflict):
		return http.StatusConflict, ErrorResponse{ErrorCode: "concurrency_conflict", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "something went wrong"}
	}
}
