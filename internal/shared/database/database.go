package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	migrate "github.com/rubenv/sql-migrate"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return pool, nil
}

func Migrate(databaseURL string) (int, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return 0, fmt.Errorf("opening database for migration: %w", err)
	}
	defer db.Close()

	migrations := migrate.EmbedFileSystemMigrationSource{
		FileSystem: migrationFS,
		Root:       "migrations",
	}

	n, err := migrate.Exec(db, "postgres", migrations, migrate.Up)
	if err != nil {
		return 0, fmt.Errorf("running migrations: %w", err)
	}
	return n, nil
}
