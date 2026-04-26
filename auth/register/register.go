package register

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/auth"
)

func RegisterRoutes(router *gin.Engine, store auth.UserStorer) {
	handler := NewHandler(store)
	router.POST("/auth/register", handler.Register)
}
