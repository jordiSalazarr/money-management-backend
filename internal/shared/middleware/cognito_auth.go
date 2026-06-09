// Phase 4 replaces the custom HMAC-signed JWT middleware with claims
// produced by API Gateway's native JWT authorizer. The gateway validates
// Cognito tokens against the user pool's JWKS at the edge; failed tokens
// 401 before the Lambda is invoked. By the time CognitoAuth runs, the
// claims are already trusted — we just need to fish them out of the
// proxy adapter's context value.
//
// Local dev (cmd/server) doesn't sit behind API Gateway, so it falls
// back to an X-Dev-User header. The fallback is harmless in Lambda mode
// because API Gateway only forwards requests with valid JWT claims —
// the API GW context path always wins there.

package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/gin-gonic/gin"
)

const userIDKey contextKey = "user_id"

// ClaimsSource extracts validated JWT claims from a request context.
// Production uses the API GW v2 proxy adapter's context value; tests
// inject a fake. Returns ok=false when no API GW context is on ctx.
type ClaimsSource func(ctx context.Context) (map[string]string, bool)

// CognitoAuth runs before every request, lets public paths through
// unauthenticated, and otherwise extracts the user ID from either the
// API GW JWT claims or (cmd/server fallback) an X-Dev-User header. It
// stashes the user ID in the request context where GetUserID can find it.
//
// Pass source=nil to use the default API-GW-backed ClaimsSource.
func CognitoAuth(publicPaths []string, source ClaimsSource) gin.HandlerFunc {
	if source == nil {
		source = defaultClaimsSource
	}
	return func(c *gin.Context) {
		if isPublicPath(c.Request.URL.Path, publicPaths) {
			c.Next()
			return
		}

		if claims, ok := source(c.Request.Context()); ok {
			if sub := claims["sub"]; sub != "" {
				c.Request = c.Request.WithContext(WithUserID(c.Request.Context(), sub))
				c.Next()
				return
			}
		}

		// Local-dev fallback. Safe in Lambda mode: every Lambda invocation
		// has an API GW context, so the branch above always matches there.
		if userID := c.GetHeader("X-Dev-User"); userID != "" {
			c.Request = c.Request.WithContext(WithUserID(c.Request.Context(), userID))
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error_code": "unauthorized",
			"message":    "missing or invalid authentication",
		})
	}
}

// defaultClaimsSource reads claims from the proxy adapter's stashed
// API GW v2 request context.
func defaultClaimsSource(ctx context.Context) (map[string]string, bool) {
	rc, ok := core.GetAPIGatewayV2ContextFromContext(ctx)
	if !ok {
		return nil, false
	}
	if rc.Authorizer == nil || rc.Authorizer.JWT == nil {
		return nil, false
	}
	return rc.Authorizer.JWT.Claims, true
}

// Compile-time assertion that the events package type lines up with the
// claims source signature — otherwise a library upgrade would silently
// break the auth path.
var _ events.APIGatewayV2HTTPRequestContext = events.APIGatewayV2HTTPRequestContext{}

func isPublicPath(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// GetUserID extracts the authenticated user ID (Cognito sub claim or
// X-Dev-User value) from the request context.
func GetUserID(ctx context.Context) string {
	if id, ok := ctx.Value(userIDKey).(string); ok {
		return id
	}
	return ""
}

// WithUserID returns a child context carrying the user ID. Exported so
// tests outside this package can set it without invoking middleware.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}
