package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// projectionTables maps a projection to the table it owns.
//
// Replaying one means emptying its table and forgetting its position, so the
// mapping has to be exact: truncating the wrong table would empty a read model
// whose checkpoint still says it is up to date, and nothing would ever rebuild
// it.
var projectionTables = map[string]string{
	"wallets_projector":             "wallets_view",
	"movements_projector":           "movements",
	"debts_projector":               "debts_view",
	"recurring_movements_projector": "recurring_movements_view",
	"processes_projector":           "processes_view",
}

// runReplay rebuilds one projection from the beginning of the log.
//
// This is the operation independent checkpoints exist for (ADR 0003). With a
// single shared position, rebuilding one read model would mean replaying all
// of them; here it is two statements in one transaction, and the other four
// keep serving throughout.
//
// The two statements must be one transaction. Truncating without clearing the
// checkpoint leaves an empty table that the projector believes is current — it
// would sit there, caught up, serving nothing, and no error anywhere would say
// so. That is strictly worse than not replaying at all.
func runReplay(ctx context.Context, projection string) error {
	table, ok := projectionTables[projection]
	if !ok {
		return fmt.Errorf("unknown projection %q\n\nknown projections:\n%s",
			projection, knownProjections())
	}

	url := envOr("AURORA_URL", "postgres://money:money@localhost:5432/money_management?sslmode=disable")
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s", table)); err != nil {
		return fmt.Errorf("truncating %s: %w", table, err)
	}
	if _, err := tx.Exec(ctx,
		"DELETE FROM projection_checkpoints WHERE subscription_name = $1", projection); err != nil {
		return fmt.Errorf("clearing the checkpoint for %s: %w", projection, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	fmt.Fprintf(os.Stdout,
		"%s: emptied %s and cleared its checkpoint.\nRestart the projector and it will rebuild from the start of the log. The other projections are untouched.\n",
		projection, table)
	return nil
}

func knownProjections() string {
	names := make([]string, 0, len(projectionTables))
	for name, table := range projectionTables {
		names = append(names, fmt.Sprintf("  %-30s -> %s", name, table))
	}
	return strings.Join(names, "\n")
}
