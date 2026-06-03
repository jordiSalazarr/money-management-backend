package login

import (
	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/auth"
)

func RegisterRoutes(router *gin.Engine, store auth.UserStorer, jwtSecret string) {
	handler := NewHandler(store, jwtSecret)
	router.POST("/auth/login", handler.Login)
}
