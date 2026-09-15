// Command recurring-cron opens an occurrence for every recurring movement
// whose next due date has passed, then exits.
//
// It only OFFERS the work. Whether the money can actually move is the
// occurrence reactor's business, and its outcome — executed or skipped — is
// recorded there. That separation is the fix for the old behaviour, where the
// cron marked a date executed before anything had been paid.
//
// Repeat runs are safe by construction: an occurrence's stream is named from
// the recurring movement and the date, and is opened at ExpectedRevision
// NoStream, so a second run for the same date collides instead of firing
// twice. A run that missed its slot catches up rather than skipping, because
// the query matches every date that has passed, not only today's.
package main

import (
	"context"
	"encoding/json"
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
	bundle, err := app.BuildRecurringCron(ctx, logr, app.RecurringCronOptions{})
	if err != nil {
		log.Fatalf("build recurring cron: %v", err)
	}
	defer bundle.Cleanup()

	resp, err := bundle.Run(ctx)
	if err != nil {
		logr.Error("recurring cron failed", "error", err)
		os.Exit(1)
	}

	out, _ := json.Marshal(resp)
	os.Stdout.Write(append(out, '\n'))

	if len(resp.Failures) > 0 {
		logr.Error("recurring cron finished with failures", "count", len(resp.Failures))
		os.Exit(1)
	}
}
