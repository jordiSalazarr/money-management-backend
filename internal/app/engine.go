package app

import (
	"context"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/jordisalazar/money-management/internal/shared/middleware"
	"github.com/jordisalazar/money-management/internal/voice"
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
	r.Use(middleware.CognitoAuth([]string{"/swagger/"}, nil))

	mountQueries := opts.MountQueryRoutes && d.queriesReady

	// Voice slice picks its backend from env vars. ANTHROPIC_API_KEY
	// signals local-dev mode (use the direct API path); otherwise we
	// fall through to Bedrock and rely on the default AWS credential
	// chain + the IAM policy the infra attaches.
	voiceOpts := voice.ClientOptions{
		BedrockModelID:  os.Getenv("BEDROCK_MODEL_ID"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
	}
	mountVoice := mountQueries // voice depends on projection stores too
	logVoiceDecision(log, mountQueries, voiceOpts)

	registerRoutes(ctx, r, d.store, voiceOpts, mountVoice, d.queries, applyMountQueries(opts, mountQueries))

	return r, cleanup, nil
}

// applyMountQueries returns a copy of opts with MountQueryRoutes set to
// the effective value (the requested flag AND the deps actually being
// ready). Avoids the registerRoutes caller having to mutate opts.
func applyMountQueries(opts Options, mountQueries bool) Options {
	opts.MountQueryRoutes = mountQueries
	return opts
}

func logVoiceDecision(log logger.Logger, mountQueries bool, opts voice.ClientOptions) {
	switch {
	case !mountQueries:
		log.Info("voice assistant disabled (projection stores not available)")
	case opts.AnthropicAPIKey != "":
		log.Info("voice assistant enabled — direct Anthropic API (local dev)")
	default:
		log.Info("voice assistant enabled — Bedrock", "model", opts.BedrockModelID)
	}
}
