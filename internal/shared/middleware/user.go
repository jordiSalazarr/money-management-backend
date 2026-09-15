package middleware

import (
	"context"
	"strings"
)

const userIDKey contextKey = "user_id"

// WithUserID puts the authenticated user on the context. Only Auth calls it in
// production; tests use it to stand in for a login.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// GetUserID returns the authenticated user, or "" if the request was not
// authenticated. Handlers treat "" as unauthenticated rather than as a user.
func GetUserID(ctx context.Context) string {
	if id, ok := ctx.Value(userIDKey).(string); ok {
		return id
	}
	return ""
}

// isPublicPath reports whether a path is exempt from authentication.
//
// Prefix matching, so "/health/" covers both probes without listing each.
func isPublicPath(path string, publicPaths []string) bool {
	for _, p := range publicPaths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
