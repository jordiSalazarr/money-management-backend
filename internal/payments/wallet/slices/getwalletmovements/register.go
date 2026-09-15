package getwalletmovements

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
)

func RegisterQueries(router *gin.Engine, store projections.MovementStore) {
	handler := NewHandler(store)

	router.GET("/wallets/:id/movements", handler.GetWalletMovements)
}
