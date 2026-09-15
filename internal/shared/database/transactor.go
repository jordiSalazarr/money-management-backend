package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolTransactor runs work inside a Postgres transaction.
type PoolTransactor struct {
	pool *pgxpool.Pool
}

func NewPoolTransactor(pool *pgxpool.Pool) *PoolTransactor {
	return &PoolTransactor{pool: pool}
}

// InTx begins a transaction, runs fn against it, and commits — rolling back if
// fn fails.
//
// This is what makes a projection effectively exactly-once: the rows it writes
// and the checkpoint recording that it wrote them land together or not at all.
// A crash in between leaves the position where it was, so the event is simply
// applied again, and the projections' deterministic row ids make that a no-op.
func (t *PoolTransactor) InTx(ctx context.Context, fn func(db DBTX) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	// Rollback after a successful commit is a no-op that returns
	// pgx.ErrTxClosed, so this is safe as an unconditional defer.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil && err != pgx.ErrTxClosed {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}
