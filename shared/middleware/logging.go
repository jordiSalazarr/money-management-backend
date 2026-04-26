package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/pkg/logger"
)

func Logging(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		correlationID := GetCorrelationID(c.Request.Context())
		log.Info("http request",
			"correlation_id", correlationID,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}
