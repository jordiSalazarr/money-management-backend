package getwalletmovements

import "github.com/gin-gonic/gin"

func RegisterQueries(router *gin.Engine, projector *MovementsProjector) {
	handler := NewHandler(projector)

	router.GET("/wallets/:id/movements", handler.GetWalletMovements)
}
