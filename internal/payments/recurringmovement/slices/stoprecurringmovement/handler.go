package stoprecurringmovement

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

type Request struct {
	Reason string `json:"reason"`
}

type Response struct {
	RecurringMovementID string `json:"recurring_movement_id"`
	StoppedAt           string `json:"stopped_at"`
}

type ErrorResponse struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// StopRecurringMovement stops an active recurring movement.
// @Summary      Stop a recurring movement
// @Description  Stops an active recurring movement so it will no longer be executed.
// @Tags         RecurringMovements
// @Accept       json
// @Produce      json
// @Param        id    path      string   true  "Recurring movement ID"
// @Param        body  body      Request  false "Optional reason"
// @Success      200   {object}  Response
// @Failure      400   {object}  ErrorResponse  "not_active"
// @Failure      403   {object}  ErrorResponse  "not_owner"
// @Failure      404   {object}  ErrorResponse  "recurring_movement_not_found"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /recurring-movements/{id}/stop [post]
func (h *Handler) StopRecurringMovement(c *gin.Context) {
	id := c.Param("id")

	var req Request
	// Body is optional — ignore bind errors for empty body
	_ = c.ShouldBindJSON(&req)

	cmd := Command{
		RecurringMovementID: id,
		StoppedBy:           middleware.GetUserID(c.Request.Context()),
		Reason:              req.Reason,
	}

	stoppedAt, err := h.cmdHandler.Handle(c.Request.Context(), cmd)
	if err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusOK, Response{
		RecurringMovementID: id,
		StoppedAt:           stoppedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, eventstore.ErrAggregateNotFound):
		return http.StatusNotFound, ErrorResponse{ErrorCode: "recurring_movement_not_found", Message: "recurring movement not found"}
	case errors.Is(err, recurringmovement.ErrNotActive):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "not_active", Message: err.Error()}
	case errors.Is(err, recurringmovement.ErrNotOwner):
		return http.StatusForbidden, ErrorResponse{ErrorCode: "not_owner", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
