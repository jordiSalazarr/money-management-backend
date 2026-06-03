package middleware

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type contextKey string

const correlationIDKey contextKey = "correlation_id"

func Correlation() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.New().String()
		c.Set(string(correlationIDKey), id)
		c.Request = c.Request.WithContext(
			context.WithValue(c.Request.Context(), correlationIDKey, id),
		)
		c.Header("X-Correlation-ID", id)
		c.Next()
	}
}

func GetCorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}
	return ""
}
