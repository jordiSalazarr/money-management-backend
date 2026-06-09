// cmd/reactor is the Kinesis-triggered Lambda that consumes the same
// DynamoDB → Kinesis stream the projector reads and turns DebtSettled +
// RecurringMovementExecuted events into wallet deposit / withdrawal
// commands. Idempotency keys flow through to the wallet aggregate so
// Kinesis retries cannot double-spend.
//
// init() wires the router once per cold start. The handler is a thin
// pass-through to Router.HandleBatch.
package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/jordisalazar/money-management/internal/app"
	"github.com/jordisalazar/money-management/pkg/logger"
)

var bundle *app.ReactorBundle

func init() {
	if os.Getenv("SKIP_LAMBDA_INIT") == "1" {
		return
	}
	logr := logger.New()
	b, err := app.BuildReactor(context.Background(), logr, app.ReactorOptions{})
	if err != nil {
		log.Fatalf("build reactor: %v", err)
	}
	bundle = b
}

func handler(ctx context.Context, e events.KinesisEvent) error {
	return bundle.Router.HandleBatch(ctx, e.Records)
}

func main() {
	lambda.Start(handler)
}
