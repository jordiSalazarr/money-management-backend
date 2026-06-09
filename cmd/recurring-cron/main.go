// cmd/recurring-cron is the EventBridge-triggered Lambda that scans
// Aurora's recurring_movements_view for items whose next_due_date has
// passed and dispatches an ExecuteRecurringMovement command for each.
//
// The recurring aggregate's ErrAlreadyExecutedToday and ErrNotActive
// guards make repeat invocations safe — the cron firing at 00:00 UTC
// the next day still picks up any rows that yesterday's firing missed.
//
// The handler returns a small JSON envelope with counts so a manual
// `aws lambda invoke` shows what happened.
package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/jordisalazar/money-management/internal/app"
	"github.com/jordisalazar/money-management/pkg/logger"
)

var bundle *app.RecurringCronBundle

func init() {
	if os.Getenv("SKIP_LAMBDA_INIT") == "1" {
		return
	}
	logr := logger.New()
	b, err := app.BuildRecurringCron(context.Background(), logr, app.RecurringCronOptions{})
	if err != nil {
		log.Fatalf("build recurring cron: %v", err)
	}
	bundle = b
}

func handler(ctx context.Context) (app.RecurringCronResponse, error) {
	return bundle.Run(ctx)
}

func main() {
	lambda.Start(handler)
}
