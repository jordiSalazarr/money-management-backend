package recorddebt

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

func RegisterCommands(router *gin.Engine, store eventstore.Store) {
	cmdHandler := NewCommandHandler(store)
	handler := NewHandler(cmdHandler)

	router.POST("/debts", handler.RecordDebt)
}
