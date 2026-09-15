package gettransfer

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

func RegisterQueries(router *gin.Engine, store eventstore.Store, codec *eventstore.Codec) {
	handler := NewHandler(store, codec)
	router.GET("/transfers/:id", handler.GetTransfer)
}
