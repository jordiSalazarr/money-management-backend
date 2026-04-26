package getusermovements

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/wallet/addwallet"
	"github.com/jordisalazar/money-management/wallet/getwalletmovements"
)

func RegisterQueries(router *gin.Engine, walletsProjector *addwallet.WalletsProjector, movementsProjector *getwalletmovements.MovementsProjector) {
	handler := NewHandler(walletsProjector, movementsProjector)

	router.GET("/movements", handler.GetUserMovements)
}
