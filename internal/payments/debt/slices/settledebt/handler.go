package settledebt

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

// Request is the payload for settling a debt.
type Request struct {
	SettledBy string `json:"settled_by" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// Response is the result of settling a debt.
type Response struct {
	DebtID string `json:"debt_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Status string `json:"status" example:"settled"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"debt_already_settled"`
	Message   string `json:"message" example:"debt is already settled"`
}

// SettleDebt marks a debt as settled.
// @Summary      Settle a debt
// @Description  Marks a debt as settled and triggers a wallet deposit or withdrawal via outbox.
// @Tags         Debts
// @Accept       json
// @Produce      json
// @Param        id    path      string   true  "Debt ID"
// @Param        body  body      Request  true  "Settle payload"
// @Success      200   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error (missing_settled_by, debt_already_settled)"
// @Failure      401   {object}  ErrorResponse  "unauthorized"
// @Failure      404   {object}  ErrorResponse  "debt_not_found"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /debts/{id}/settle [post]
func (h *Handler) SettleDebt(c *gin.Context) {
	debtID := c.Param("id")

	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	cmd := Command{
		DebtID:    debtID,
		SettledBy: req.SettledBy,
	}

	if err := h.cmdHandler.Handle(c.Request.Context(), cmd); err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusOK, Response{
		DebtID: debtID,
		Status: "settled",
	})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, debt.ErrMissingSettledBy):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_settled_by", Message: err.Error()}
	case errors.Is(err, debt.ErrDebtAlreadySettled):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "debt_already_settled", Message: err.Error()}
	case errors.Is(err, debt.ErrSettlementInProgress):
		// Distinct from already-settled: the money has not moved yet, the
		// reactor is still working on it, and it may yet fail. Answering
		// "already settled" here would be the same lie this process exists to
		// remove.
		return http.StatusConflict, ErrorResponse{ErrorCode: "settlement_in_progress", Message: err.Error()}
	case errors.Is(err, eventstore.ErrAggregateNotFound):
		return http.StatusNotFound, ErrorResponse{ErrorCode: "debt_not_found", Message: "debt not found"}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
