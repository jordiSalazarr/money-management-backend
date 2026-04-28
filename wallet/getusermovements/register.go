package getusermovements

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/wallet/projections"
)

func RegisterQueries(router *gin.Engine, walletStore projections.WalletStore, movementStore projections.MovementStore) {
	handler := NewHandler(walletStore, movementStore)

	router.GET("/movements", handler.GetUserMovements)
}
