package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Auth authenticates every request that is not on a public path.
//
// It replaces the API-Gateway-shaped middleware, which trusted claims it was
// handed because the gateway had already validated them. Behind an Ingress
// nothing validates anything, so the same code would have left the X-Dev-User
// header as the only authentication — and that header is self-asserted. Anyone
// could have been anyone.
//
// The two modes are mutually exclusive, deliberately:
//
//   - No issuer configured: local development. The X-Dev-User header is
//     accepted and no token is verified.
//   - An issuer configured: the header is REFUSED, whatever it says, and only
//     a signed token from that issuer will do.
//
// Whichever mode is in play, the identity is put through stableUserID before
// it reaches the application, because the read models store user ids as
// Postgres uuids and most providers' subjects are not UUIDs.
//
// Making them exclusive rather than a fallback chain is the whole safety
// property. A fallback would mean that a deployment where the token check
// failed — a misconfigured issuer, an unreachable JWKS — silently degraded to
// "trust whatever header you are sent" instead of failing.
func Auth(publicPaths []string, verifier *OIDCVerifier) gin.HandlerFunc {
	oidcEnabled := verifier != nil && verifier.config.Enabled()

	return func(c *gin.Context) {
		if isPublicPath(c.Request.URL.Path, publicPaths) {
			c.Next()
			return
		}

		if oidcEnabled {
			sub, err := verifier.Verify(c.Request.Context(), bearerToken(c))
			if err != nil {
				unauthorized(c, "invalid or missing bearer token")
				return
			}
			c.Request = c.Request.WithContext(WithUserID(c.Request.Context(), stableUserID(verifier.config.Issuer, sub)))
			c.Next()
			return
		}

		if userID := c.GetHeader("X-Dev-User"); userID != "" {
			c.Request = c.Request.WithContext(WithUserID(c.Request.Context(), stableUserID("", userID)))
			c.Next()
			return
		}

		unauthorized(c, "missing or invalid authentication")
	}
}

func bearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return header[7:]
	}
	return ""
}

func unauthorized(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error_code": "unauthorized",
		"message":    message,
	})
}
