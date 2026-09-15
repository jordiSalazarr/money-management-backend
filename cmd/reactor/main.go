// Command reactor subscribes to the event log and drives the process managers
// that react to it: transfers, recurring-movement occurrences, and debt
// settlements.
//
// Like the projector, it used to be a Kinesis-fed Lambda and is now a
// long-running subscriber, and it refuses to start against a store with no
// global log.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jordisalazar/money-management/internal/app"
	"github.com/jordisalazar/money-management/pkg/logger"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logr := logger.New()
	bundle, err := app.BuildReactor(ctx, logr, app.ReactorOptions{})
	if err != nil {
		log.Fatalf("build reactor: %v", err)
	}
	defer bundle.Cleanup()

	logr.Info("reactor running")
	if err := bundle.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logr.Error("reactor stopped", "error", err)
		os.Exit(1)
	}
	logr.Info("reactor stopped cleanly")
}
