package getrecurringmovements

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
)

func RegisterQueries(router *gin.Engine, store projections.RecurringMovementStore) {
	handler := NewHandler(store)

	router.GET("/wallets/:id/recurring-movements", handler.GetRecurringMovements)
}
