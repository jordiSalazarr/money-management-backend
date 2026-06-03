package getdebts

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/debt/projections"
)

func RegisterQueries(router *gin.Engine, store projections.DebtStore) {
	handler := NewHandler(store)

	router.GET("/debts", handler.GetDebts)
}
