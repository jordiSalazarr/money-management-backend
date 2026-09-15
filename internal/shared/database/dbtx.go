// Package database holds the Postgres plumbing shared by the read models.
package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is the slice of pgx that a projection store actually needs.
//
// Both *pgxpool.Pool and pgx.Tx satisfy it, which is the whole point: a store
// can be handed a pool when it is answering a query, and a transaction when it
// is being written to alongside a checkpoint. Without this the rows and the
// checkpoint could not commit together, and a crash between them would either
// lose work or replay it.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
