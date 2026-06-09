// cmd/lambda is the API Gateway HTTP API → Lambda entry point.
//
// Init-time wiring (build the gin engine once per cold start) amortises
// cost across warm invocations. The same gin engine that cmd/server runs
// is wrapped in an APIGatewayV2 adapter, so middleware and slice handlers
// see a normal *http.Request and behave identically to the HTTP server.
//
// Failure to construct the engine in init() panics the function — every
// invocation will 500 until a fixed binary redeploys. That's intentional:
// silent degradation (e.g. running without a Dynamo client) would be
// strictly worse.
package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"

	"github.com/jordisalazar/money-management/internal/app"
	"github.com/jordisalazar/money-management/pkg/logger"
)

var ginLambda *ginadapter.GinLambdaV2

func init() {
	// Skip wiring during `go test` / `go vet` — init() runs before tests
	// too, and we don't want a test process to try to reach DynamoDB.
	if os.Getenv("SKIP_LAMBDA_INIT") == "1" {
		return
	}

	logr := logger.New()
	engine, _, err := app.BuildEngine(context.Background(), logr, app.Options{
		StartBackgroundJobs: false,
		MountQueryRoutes:    false,
		DefaultStore:        app.StoreModeDynamoDB,
	})
	if err != nil {
		log.Fatalf("build engine: %v", err)
	}
	ginLambda = ginadapter.NewV2(engine)
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return ginLambda.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(handler)
}
