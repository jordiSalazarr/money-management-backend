// cmd/projector is the Kinesis-triggered Lambda that consumes the
// DynamoDB → Kinesis Data Streams export from the events table and
// writes read-models into Aurora.
//
// The same per-shard ordering guarantee that DynamoDB provides for
// version writes inside one aggregate flows through to Kinesis because
// the shard hashing uses the DDB partition key (aggregate_id) — every
// event for one aggregate lands on the same shard and is delivered in
// order. Projectors don't need cross-aggregate ordering.
//
// Init-time wiring (pool + dispatcher) amortises across warm
// invocations. Failure to construct panics; the function 500s every
// invocation until a fixed binary redeploys, which is the right outcome
// because silently running without Aurora would corrupt nothing on the
// write path but mean projections fall behind invisibly.
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

var bundle *app.ProjectorBundle

func init() {
	if os.Getenv("SKIP_LAMBDA_INIT") == "1" {
		return
	}

	logr := logger.New()
	b, err := app.BuildProjector(context.Background(), logr, app.ProjectorOptions{})
	if err != nil {
		log.Fatalf("build projector: %v", err)
	}
	bundle = b
}

func handler(ctx context.Context, e events.KinesisEvent) error {
	return bundle.Dispatcher.HandleBatch(ctx, e.Records)
}

func main() {
	lambda.Start(handler)
}
