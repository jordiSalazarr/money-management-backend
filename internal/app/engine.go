package app

import (
	"context"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/jordisalazar/money-management/internal/shared/middleware"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// BuildEngine wires the full HTTP stack and returns the gin engine plus
// a cleanup func that callers should defer.
//
// The shape of the returned closure (nil-safe, always returnable) lets
// the Lambda entry point ignore it without special-casing — cmd/server
// just defers it like a Close().
func BuildEngine(ctx context.Context, log logger.Logger, opts Options) (*gin.Engine, func(), error) {
	mode, err := resolveStoreMode(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving STORE: %w", err)
	}
	log.Info("event store mode", "mode", string(mode))

	d, cleanup, err := buildDeps(ctx, log, mode)
	if err != nil {
		return nil, nil, err
	}

	r := gin.New()
	r.Use(middleware.CORS())
	r.Use(middleware.Correlation())
	r.Use(middleware.Logging(log))
	// Phase 4: API Gateway's JWT authorizer validates Cognito tokens at
	// the edge; this middleware just extracts the sub claim from the
	// proxy adapter's stashed context value. Swagger UI is the only
	// route allowed through unauthenticated.
	r.Use(middleware.CognitoAuth([]string{"/swagger/"}, nil))

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	mountQueries := opts.MountQueryRoutes && d.queriesReady
	switch {
	case anthropicKey == "":
		log.Info("voice assistant disabled (ANTHROPIC_API_KEY not set)")
	case mountQueries:
		log.Info("voice assistant enabled")
	default:
		log.Info("voice assistant disabled (projection stores not available in this mode)")
	}

	effectiveOpts := opts
	effectiveOpts.MountQueryRoutes = mountQueries
	registerRoutes(r, d.store, anthropicKey, d.queries, effectiveOpts)

	return r, cleanup, nil
}
