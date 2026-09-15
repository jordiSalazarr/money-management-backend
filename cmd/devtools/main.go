// cmd/devtools is the local-dev companion binary. Two subcommands:
//
//   reset — wipe every state surface the dev stack touches so the next
//           boot starts from a known empty world. Drops + recreates the
//           LocalStack events table, truncates the projection tables in
//           Postgres, and runs migrations to repopulate schema-only.
//
//   seed  — populate the system with a handful of realistic wallets,
//           movements, and a recurring movement so the UI is immediately
//           usable. Operates over HTTP against a running cmd/server, so
//           the seed code goes through the exact same command handlers
//           production traffic does (no second wiring path to drift).
//
// Both subcommands honour the same env vars cmd/server reads
// (EVENTS_TABLE, AURORA_URL, AWS_*), with safe defaults matching the
// Makefile's localstack targets.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jordisalazar/money-management/internal/shared/database"
)

const devUserHeader = "X-Dev-User"

// devUserID is a fixed UUID rather than a friendly slug because the
// projection schema models user_id as a Postgres uuid column (Cognito
// sub claims are UUIDs in prod). Must match DEV_USER_ID in web/src/auth.
const devUserID = "00000000-0000-0000-0000-000000000001"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "reset":
		if err := runReset(context.Background()); err != nil {
			log.Fatalf("reset: %v", err)
		}
	case "seed":
		if err := runSeed(context.Background()); err != nil {
			log.Fatalf("seed: %v", err)
		}
	case "migrate":
		if err := runMigrate(); err != nil {
			log.Fatalf("migrate: %v", err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: devtools <reset|seed|migrate>")
}

// runMigrate applies any unapplied migrations. Idempotent — sql-migrate
// tracks applied versions in gorp_migrations.
func runMigrate() error {
	url := envOr("AURORA_URL", "postgres://money:money@localhost:5432/money_management?sslmode=disable")
	applied, err := database.Migrate(url)
	if err != nil {
		return err
	}
	log.Printf("migrate: applied %d migrations", applied)
	return nil
}

// runReset wipes both event store and projections so the next boot
// starts from zero. Order matters: drop projections first, then events;
// otherwise re-running migrations while the events table is being
// recreated would race.
func runReset(ctx context.Context) error {
	auroraURL := envOr("AURORA_URL", "postgres://money:money@localhost:5432/money_management?sslmode=disable")
	eventsTable := envOr("EVENTS_TABLE", "mm-events-dev")

	if err := resetProjections(ctx, auroraURL); err != nil {
		return fmt.Errorf("resetting projections: %w", err)
	}
	if err := resetEventsTable(ctx, eventsTable); err != nil {
		return fmt.Errorf("resetting events table: %w", err)
	}
	log.Println("reset: complete")
	return nil
}

// resetProjections drops the projection tables + the migration tracking
// table and re-runs migrations. Dropping the tracking table is what
// makes the rerun see every migration as unapplied.
func resetProjections(ctx context.Context, dbURL string) error {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	defer pool.Close()

	tables := []string{
		"movements",
		"wallets_view",
		"transactions_view",
		"debts_view",
		"recurring_movements_view",
		"gorp_migrations",
	}
	for _, t := range tables {
		if _, err := pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", t)); err != nil {
			return fmt.Errorf("dropping %s: %w", t, err)
		}
	}
	log.Println("reset: dropped projection tables")

	applied, err := database.Migrate(dbURL)
	if err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	log.Printf("reset: applied %d migrations", applied)
	return nil
}

// resetEventsTable drops + recreates the LocalStack events table. The
// schema mirrors what backend/Makefile's create-local-tables target sets
// up so dev-stack and reset stay in sync.
func resetEventsTable(ctx context.Context, table string) error {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}
	client := dynamodb.NewFromConfig(cfg)

	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)})
	if err != nil {
		var rnf *types.ResourceNotFoundException
		if !errors.As(err, &rnf) {
			return fmt.Errorf("deleting %s: %w", table, err)
		}
	}

	_, err = client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(table),
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("aggregate_id"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("version"), AttributeType: types.ScalarAttributeTypeN},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("aggregate_id"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("version"), KeyType: types.KeyTypeRange},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	if err != nil {
		return fmt.Errorf("creating %s: %w", table, err)
	}
	log.Printf("reset: recreated %s", table)
	return nil
}

// ---------------------------------------------------------------------
// seed
// ---------------------------------------------------------------------

// runSeed creates a small but realistic world: two wallets, a couple of
// movements on each, a transfer between them, and a recurring movement.
// Every write goes through the running cmd/server's HTTP API with the
// dev user header, so the seed exercises the same code path real
// requests do.
func runSeed(ctx context.Context) error {
	apiURL := envOr("API_URL", "http://localhost:8019")

	if err := waitForServer(ctx, apiURL, 30*time.Second); err != nil {
		return fmt.Errorf("waiting for server: %w", err)
	}

	cash, err := createWallet(ctx, apiURL, walletReq{
		Name: "Cash", Description: "Pocket money", InitialAmount: 20000, Currency: "EUR", Category: "Cash",
	})
	if err != nil {
		return fmt.Errorf("creating Cash wallet: %w", err)
	}
	log.Printf("seed: created wallet Cash %s", cash)

	bank, err := createWallet(ctx, apiURL, walletReq{
		Name: "Bank", Description: "Checking account", InitialAmount: 100000, Currency: "EUR", Category: "Checking",
	})
	if err != nil {
		return fmt.Errorf("creating Bank wallet: %w", err)
	}
	log.Printf("seed: created wallet Bank %s", bank)

	if err := deposit(ctx, apiURL, bank, 250000, "Monthly salary", "Salary"); err != nil {
		return fmt.Errorf("depositing salary: %w", err)
	}
	log.Println("seed: deposited 2500.00 salary to Bank")

	if err := withdraw(ctx, apiURL, cash, 3500, "Weekly groceries", "Food & Groceries"); err != nil {
		return fmt.Errorf("withdrawing groceries: %w", err)
	}
	log.Println("seed: withdrew 35.00 groceries from Cash")

	if err := transfer(ctx, apiURL, bank, cash, 50000, "Top up wallet"); err != nil {
		return fmt.Errorf("transferring bank → cash: %w", err)
	}
	log.Println("seed: transferred 500.00 Bank → Cash")

	log.Println("seed: complete")
	return nil
}

type walletReq struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	InitialAmount int    `json:"initial_amount"`
	Currency      string `json:"currency"`
	Category      string `json:"category"`
}

type walletResp struct {
	WalletID string `json:"wallet_id"`
}

func createWallet(ctx context.Context, apiURL string, req walletReq) (string, error) {
	var resp walletResp
	if err := postJSON(ctx, apiURL+"/wallets", req, &resp); err != nil {
		return "", err
	}
	return resp.WalletID, nil
}

func deposit(ctx context.Context, apiURL, walletID string, amount int, reason, category string) error {
	body := map[string]any{
		"amount": amount, "currency": "EUR", "reason": reason, "category": category,
	}
	return postJSON(ctx, fmt.Sprintf("%s/wallets/%s/deposit", apiURL, walletID), body, nil)
}

func withdraw(ctx context.Context, apiURL, walletID string, amount int, reason, category string) error {
	body := map[string]any{
		"amount": amount, "currency": "EUR", "reason": reason, "category": category,
	}
	return postJSON(ctx, fmt.Sprintf("%s/wallets/%s/withdraw", apiURL, walletID), body, nil)
}

func transfer(ctx context.Context, apiURL, fromID, toID string, amount int, reason string) error {
	body := map[string]any{
		"from_wallet_id": fromID, "to_wallet_id": toID,
		"amount": amount, "currency": "EUR", "reason": reason,
	}
	return postJSON(ctx, apiURL+"/transfers", body, nil)
}

// postJSON sends body as JSON and decodes the response into out if
// non-nil. 4xx/5xx responses come back as errors carrying the server's
// error envelope so a misconfigured seed prints something actionable.
func postJSON(ctx context.Context, url string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(devUserHeader, devUserID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST %s → %d: %s", url, resp.StatusCode, string(respBody))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding response: %w (body=%s)", err, string(respBody))
		}
	}
	return nil
}

// waitForServer polls /swagger/ until the server responds with anything
// other than a connection error. /swagger/ is on the public-paths
// allowlist so it works regardless of auth state.
func waitForServer(ctx context.Context, apiURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/swagger/index.html", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("server not reachable at %s after %s", apiURL, timeout)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
