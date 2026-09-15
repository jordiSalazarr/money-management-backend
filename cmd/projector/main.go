// Command projector subscribes to the event log and keeps the read models up
// to date.
//
// Each projection runs its own subscription from its own checkpoint, so one
// falling behind or being rebuilt does not hold up the others.
//
// It also serves /metrics and /health/live. A projector without them can only
// be probed on whether its process is alive, which is the question that does
// not matter: a projector can be alive, healthy, and well behind, serving a
// read model that quietly disagrees with the log. Lag is the honest signal.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jordisalazar/money-management/internal/app"
	"github.com/jordisalazar/money-management/pkg/logger"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logr := logger.New()
	bundle, err := app.BuildProjector(ctx, logr, app.ProjectorOptions{})
	if err != nil {
		log.Fatalf("build projector: %v", err)
	}
	defer bundle.Cleanup()

	metrics := &http.Server{
		Addr:              metricsAddr(),
		Handler:           bundle.Lag.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := metrics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logr.Error("metrics server stopped", "error", err)
		}
	}()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = metrics.Shutdown(shutdown)
	}()

	logr.Info("projector running", "projections", len(bundle.Runners), "metrics", metrics.Addr)
	if err := bundle.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logr.Error("projector stopped", "error", err)
		os.Exit(1)
	}
	logr.Info("projector stopped cleanly")
}

func metricsAddr() string {
	if port := os.Getenv("METRICS_PORT"); port != "" {
		return ":" + port
	}
	return ":9090"
}
