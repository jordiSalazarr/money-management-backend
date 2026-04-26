package getuserwallets

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/wallet/addwallet"
)

func RegisterQueries(router *gin.Engine, projector *addwallet.WalletsProjector) {
	handler := NewHandler(projector)

	router.GET("/wallets", handler.GetUserWallets)
}
