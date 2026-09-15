// Command sweeper finds processes that have stopped making progress and
// re-drives them.
//
// It runs once and exits, because that is the shape of the job: a periodic
// check, not a service. Everything else in the process machinery assumes the
// reactor runs; this exists for the failures where nothing fails — the reactor
// was down, a message was parked, an event was never routed — and a process
// sits in a non-terminal state with no outcome event and no error anywhere.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jordisalazar/money-management/internal/app"
	"github.com/jordisalazar/money-management/pkg/logger"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	logr := logger.New()
	bundle, err := app.BuildSweeper(ctx, logr, app.SweeperOptions{
		Threshold:   durationFromEnv("SWEEP_THRESHOLD", 10*time.Minute),
		MaxAttempts: intFromEnv("SWEEP_MAX_ATTEMPTS", 3),
	})
	if err != nil {
		log.Fatalf("build sweeper: %v", err)
	}
	defer bundle.Cleanup()

	report, err := bundle.Run(ctx)
	if err != nil {
		logr.Error("sweep failed", "error", err)
		os.Exit(1)
	}

	// The report goes to stdout as JSON so `kubectl logs` on the Job shows
	// what the pass actually did rather than just that it exited 0.
	out, _ := json.Marshal(report)
	os.Stdout.Write(append(out, '\n'))

	// Individual failures do not fail the pass — one stuck process should not
	// stop the others being looked at — but they must not vanish either.
	if len(report.Failures) > 0 {
		logr.Error("sweep finished with failures", "count", len(report.Failures))
		os.Exit(1)
	}
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	if raw := os.Getenv(key); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	return fallback
}

func intFromEnv(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
