package recordrecurringmovement

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/recurringmovement"
	"github.com/jordisalazar/money-management/shared/middleware"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

// Request is the payload for recording a recurring movement.
type Request struct {
	WalletID  string `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Type      string `json:"type" example:"deposit"`
	Amount    int    `json:"amount" example:"1000"`
	Currency  string `json:"currency" example:"EUR"`
	Frequency string `json:"frequency" example:"MONTHLY"`
	Reason    string `json:"reason" example:"Salary"`
	StartDate string `json:"start_date" example:"2026-05-01"`
	EndDate   string `json:"end_date,omitempty" example:"2027-05-01"`
}

// Response is the result of recording a recurring movement.
type Response struct {
	RecurringMovementID string `json:"recurring_movement_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"invalid_amount"`
	Message   string `json:"message" example:"amount must be between 1 and 10000000"`
}

// RecordRecurringMovement creates a new recurring movement.
// @Summary      Record a recurring movement
// @Description  Creates a recurring deposit or withdrawal on a wallet. Amount in integer cents.
// @Tags         RecurringMovements
// @Accept       json
// @Produce      json
// @Param        body  body      Request  true  "Recurring movement payload"
// @Success      201   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error"
// @Failure      401   {object}  ErrorResponse  "unauthorized"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /recurring-movements [post]
func (h *Handler) RecordRecurringMovement(c *gin.Context) {
	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_start_date",
			Message:   "start_date must be a valid date (YYYY-MM-DD)",
		})
		return
	}

	var endDate time.Time
	if req.EndDate != "" {
		endDate, err = time.Parse("2006-01-02", req.EndDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				ErrorCode: "invalid_end_date",
				Message:   "end_date must be a valid date (YYYY-MM-DD)",
			})
			return
		}
	}

	cmd := Command{
		WalletID:  req.WalletID,
		UserID:    middleware.GetUserID(c.Request.Context()),
		Type:      req.Type,
		Amount:    req.Amount,
		Currency:  req.Currency,
		Frequency: req.Frequency,
		Reason:    req.Reason,
		StartDate: startDate,
		EndDate:   endDate,
	}

	id, err := h.cmdHandler.Handle(c.Request.Context(), cmd)
	if err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusCreated, Response{RecurringMovementID: id})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, recurringmovement.ErrMissingWalletID):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_wallet_id", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrMissingUserID):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_user_id", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrInvalidType):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_type", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrInvalidAmount):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_amount", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrInvalidCurrency):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_currency", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrInvalidFrequency):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_frequency", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrMissingReason):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_reason", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrInvalidStartDate):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_start_date", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrInvalidEndDate):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_end_date", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
