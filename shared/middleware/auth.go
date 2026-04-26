package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const userIDKey contextKey = "user_id"

type authErrorResponse struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/swagger/") || strings.HasPrefix(path, "/auth/") {
			c.Next()
			return
		}

		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, authErrorResponse{
				ErrorCode: "unauthorized",
				Message:   "missing authorization header",
			})
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, authErrorResponse{
				ErrorCode: "unauthorized",
				Message:   "invalid authorization header format",
			})
			return
		}

		token, err := jwt.ParseWithClaims(parts[1], &Claims{}, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, authErrorResponse{
				ErrorCode: "unauthorized",
				Message:   "invalid or expired token",
			})
			return
		}

		claims, ok := token.Claims.(*Claims)
		if !ok || claims.UserID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, authErrorResponse{
				ErrorCode: "unauthorized",
				Message:   "invalid token claims",
			})
			return
		}

		c.Request = c.Request.WithContext(
			context.WithValue(c.Request.Context(), userIDKey, claims.UserID),
		)
		c.Next()
	}
}

// Claims is the JWT claims structure used by the auth middleware.
type Claims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

// GetUserID extracts the authenticated user ID from the request context.
func GetUserID(ctx context.Context) string {
	if id, ok := ctx.Value(userIDKey).(string); ok {
		return id
	}
	return ""
}

// WithUserID injects a user ID into the context. Used by tests to bypass JWT auth.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}
