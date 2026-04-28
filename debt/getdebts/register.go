package getdebts

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/debt/projections"
)

func RegisterQueries(router *gin.Engine, store projections.DebtStore) {
	handler := NewHandler(store)

	router.GET("/debts", handler.GetDebts)
}
