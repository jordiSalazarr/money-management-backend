// Command migrate brings the read-model schema in line with
// internal/shared/database/migrations/, then exits.
//
// It runs as a Job before anything that reads the schema starts. It used to be
// a one-shot Lambda; the shape is the same, the runtime is not.
package main

import (
	"log"
	"os"

	"github.com/jordisalazar/money-management/internal/shared/database"
)

func main() {
	url := os.Getenv("AURORA_URL")
	if url == "" {
		log.Fatal("AURORA_URL must be set")
	}

	applied, err := database.Migrate(url)
	if err != nil {
		log.Fatalf("migrating: %v", err)
	}
	log.Printf("migrate: applied %d migrations", applied)
}
