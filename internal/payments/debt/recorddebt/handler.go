package recorddebt

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/debt"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

// Request is the payload for recording a debt.
type Request struct {
	WalletID  string `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Amount    int    `json:"amount" example:"1000"`
	Currency  string `json:"currency" example:"EUR"`
	Direction string `json:"direction" example:"IN"`
	Reason    string `json:"reason" example:"Dinner split"`
}

// Response is the result of recording a debt.
type Response struct {
	DebtID string `json:"debt_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"invalid_amount"`
	Message   string `json:"message" example:"amount must be between 1 and 10000000"`
}

// RecordDebt creates a new debt record.
// @Summary      Record a debt
// @Description  Records a new debt (someone owes you or you owe someone). Amount in integer cents.
// @Tags         Debts
// @Accept       json
// @Produce      json
// @Param        body  body      Request  true  "Debt recording payload"
// @Success      201   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error (invalid_amount, invalid_currency, invalid_direction, missing_reason)"
// @Failure      401   {object}  ErrorResponse  "unauthorized"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /debts [post]
func (h *Handler) RecordDebt(c *gin.Context) {
	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	cmd := Command{
		OwnerID:   middleware.GetUserID(c.Request.Context()),
		WalletID:  req.WalletID,
		Amount:    req.Amount,
		Currency:  req.Currency,
		Direction: req.Direction,
		Reason:    req.Reason,
	}

	debtID, err := h.cmdHandler.Handle(c.Request.Context(), cmd)
	if err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusCreated, Response{DebtID: debtID})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, debt.ErrMissingOwnerID):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_owner_id", Message: err.Error()}
	case errors.Is(err, debt.ErrInvalidAmount):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_amount", Message: err.Error()}
	case errors.Is(err, debt.ErrInvalidCurrency):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_currency", Message: err.Error()}
	case errors.Is(err, debt.ErrInvalidDirection):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_direction", Message: err.Error()}
	case errors.Is(err, debt.ErrMissingReason):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_reason", Message: err.Error()}
	case errors.Is(err, debt.ErrMissingWalletID):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_wallet_id", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
