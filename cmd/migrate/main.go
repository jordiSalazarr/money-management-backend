// cmd/migrate is the one-shot Lambda that brings Aurora's schema in
// line with internal/shared/database/migrations/. CI invokes it after
// each deploy; nothing else does.
//
// The handler accepts an empty payload and returns the number of
// migrations applied. Failures bubble up as Lambda invocation errors,
// which CI checks for and aborts on.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/jordisalazar/money-management/internal/shared/database"
)

type Response struct {
	Applied int `json:"applied"`
}

func handler(_ context.Context) (Response, error) {
	url := os.Getenv("AURORA_URL")
	if url == "" {
		return Response{}, fmt.Errorf("AURORA_URL must be set")
	}
	applied, err := database.Migrate(url)
	if err != nil {
		return Response{}, fmt.Errorf("migrating: %w", err)
	}
	log.Printf("migrate: applied %d migrations", applied)
	return Response{Applied: applied}, nil
}

func main() {
	lambda.Start(handler)
}
