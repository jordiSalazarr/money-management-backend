package getuserwallets

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
)

func RegisterQueries(router *gin.Engine, store projections.WalletStore) {
	handler := NewHandler(store)

	router.GET("/wallets", handler.GetUserWallets)
}
