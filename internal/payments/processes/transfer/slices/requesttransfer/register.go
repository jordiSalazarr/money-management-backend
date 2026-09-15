package requesttransfer

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

func RegisterCommands(router *gin.Engine, store eventstore.Store, codec *eventstore.Codec) {
	handler := NewHandler(NewCommandHandler(store, codec))
	router.POST("/transfers", handler.RequestTransfer)
}
