// Command projector subscribes to the event log and keeps the read models up
// to date.
//
// It used to be a Lambda fed by a DynamoDB → Kinesis export. It is now a
// long-running subscriber, which is the same thing the in-process dev
// pipeline does — so local and deployed topology finally match.
//
// It refuses to start against a store with no global log. DynamoDB is one:
// until the KurrentDB adapter lands in phase 2, this binary has nothing to
// subscribe to, and failing loudly beats silently projecting nothing.
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
	bundle, err := app.BuildProjector(ctx, logr, app.ProjectorOptions{})
	if err != nil {
		log.Fatalf("build projector: %v", err)
	}
	defer bundle.Cleanup()

	logr.Info("projector running")
	if err := bundle.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logr.Error("projector stopped", "error", err)
		os.Exit(1)
	}
	logr.Info("projector stopped cleanly")
}
