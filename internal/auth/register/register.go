package register

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/auth"
)

func RegisterRoutes(router *gin.Engine, store auth.UserStorer) {
	handler := NewHandler(store)
	router.POST("/auth/register", handler.Register)
}
