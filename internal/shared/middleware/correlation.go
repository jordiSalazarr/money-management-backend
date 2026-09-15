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

// WithCorrelationID puts a correlation id on a context that did not come from
// an HTTP request.
//
// Reactors need this: they run under a subscription, not a request, so the
// only correlation id available is the one on the event that triggered them.
// Carrying it forward is what keeps a single user action traceable across
// every stream a process touches.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationIDKey, id)
}
