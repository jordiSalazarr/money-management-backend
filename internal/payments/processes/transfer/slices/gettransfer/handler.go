// Package gettransfer serves the other half of the 202 contract: if the API
// accepts a transfer without performing it, the outcome has to be reachable.
package gettransfer

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type Handler struct {
	store eventstore.Store
	codec *eventstore.Codec
}

func NewHandler(store eventstore.Store, codec *eventstore.Codec) *Handler {
	return &Handler{store: store, codec: codec}
}

// Response reports where a transfer got to.
type Response struct {
	TransferID   string `json:"transfer_id" example:"770e8400-e29b-41d4-a716-446655440000"`
	Status       string `json:"status" example:"failed"`
	FromWalletID string `json:"from_wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	ToWalletID   string `json:"to_wallet_id" example:"660e8400-e29b-41d4-a716-446655440000"`
	Amount       int    `json:"amount" example:"3000"`
	Currency     string `json:"currency" example:"EUR"`
	Reason       string `json:"reason" example:"Rent split"`
	// FailureReason is set only for failed and stuck transfers.
	FailureReason string `json:"failure_reason,omitempty" example:"insufficient_funds"`
	Attempts      int    `json:"attempts" example:"0"`
}

type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"transfer_not_found"`
	Message   string `json:"message" example:"transfer not found"`
}

// GetTransfer reports the status of a transfer.
// @Summary      Get a transfer
// @Description  Reports where a transfer got to. "requested" and "debited" are in flight; "completed", "failed" and "stuck" are final. A failed transfer moved no money. A stuck transfer moved money that could not be returned, and needs intervention.
// @Tags         Transfers
// @Produce      json
// @Param        id   path      string  true  "Transfer ID"
// @Success      200  {object}  Response
// @Failure      404  {object}  ErrorResponse  "transfer_not_found"
// @Failure      500  {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /transfers/{id} [get]
func (h *Handler) GetTransfer(c *gin.Context) {
	transferID := c.Param("id")

	agg := transfer.NewAggregate()
	expected, err := eventstore.Rehydrate(c.Request.Context(), h.store, h.codec, streams.Transfer(transferID), agg.Apply)
	if err != nil {
		if errors.Is(err, eventstore.ErrAggregateNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{ErrorCode: "transfer_not_found", Message: "transfer not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "something went wrong"})
		return
	}
	if expected == eventstore.NoStream {
		c.JSON(http.StatusNotFound, ErrorResponse{ErrorCode: "transfer_not_found", Message: "transfer not found"})
		return
	}

	c.JSON(http.StatusOK, Response{
		TransferID:    agg.ID(),
		Status:        agg.Status(),
		FromWalletID:  agg.FromWalletID(),
		ToWalletID:    agg.ToWalletID(),
		Amount:        agg.Amount(),
		Currency:      agg.Currency(),
		Reason:        agg.Reason(),
		FailureReason: agg.FailureReason(),
		Attempts:      agg.Attempts(),
	})
}
