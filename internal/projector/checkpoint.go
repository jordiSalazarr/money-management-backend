package projector

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jordisalazar/money-management/internal/shared/database"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// CheckpointStore remembers how far each projection has read the global log.
//
// Save takes a DBTX rather than opening its own connection, because the whole
// value of a checkpoint is that it commits in the same transaction as the rows
// it accounts for. Given its own connection it could succeed while the rows
// failed, and the projection would skip that event forever.
type CheckpointStore interface {
	Load(ctx context.Context, subscription string) (eventstore.Position, error)
	Save(ctx context.Context, db database.DBTX, subscription string, at eventstore.Position) error
}

// PostgresCheckpoints is the real checkpoint store.
type PostgresCheckpoints struct {
	pool database.DBTX
}

func NewPostgresCheckpoints(pool database.DBTX) *PostgresCheckpoints {
	return &PostgresCheckpoints{pool: pool}
}

// Load returns where a subscription left off, or Start if it has never run.
//
// A missing row is not an error: a projection that has never run should read
// the log from the beginning, which is exactly what Start means.
func (s *PostgresCheckpoints) Load(ctx context.Context, subscription string) (eventstore.Position, error) {
	var commit, prepare uint64
	err := s.pool.QueryRow(ctx,
		`SELECT commit_position, prepare_position FROM projection_checkpoints WHERE subscription_name = $1`,
		subscription,
	).Scan(&commit, &prepare)

	if errors.Is(err, pgx.ErrNoRows) {
		return eventstore.Start, nil
	}
	if err != nil {
		return eventstore.Start, fmt.Errorf("loading checkpoint for %s: %w", subscription, err)
	}
	return eventstore.Position{Commit: commit, Prepare: prepare}, nil
}

func (s *PostgresCheckpoints) Save(ctx context.Context, db database.DBTX, subscription string, at eventstore.Position) error {
	_, err := db.Exec(ctx, `
		INSERT INTO projection_checkpoints (subscription_name, commit_position, prepare_position, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (subscription_name) DO UPDATE SET
			commit_position  = EXCLUDED.commit_position,
			prepare_position = EXCLUDED.prepare_position,
			updated_at       = EXCLUDED.updated_at`,
		subscription, at.Commit, at.Prepare,
	)
	if err != nil {
		return fmt.Errorf("saving checkpoint for %s: %w", subscription, err)
	}
	return nil
}

// MemoryCheckpoints is the in-process equivalent, for memory mode and tests.
//
// It is deliberately not durable: memory mode keeps no state between runs, so
// a checkpoint that survived the store it describes would point into a log
// that no longer exists.
//
// It is mutex-guarded because every projection has its own runner and they all
// share one of these — five goroutines writing one map. The Postgres store
// needs no equivalent: each write goes through its own transaction.
type MemoryCheckpoints struct {
	mu        sync.RWMutex
	positions map[string]eventstore.Position
}

func NewMemoryCheckpoints() *MemoryCheckpoints {
	return &MemoryCheckpoints{positions: make(map[string]eventstore.Position)}
}

func (s *MemoryCheckpoints) Load(_ context.Context, subscription string) (eventstore.Position, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.positions[subscription], nil
}

func (s *MemoryCheckpoints) Save(_ context.Context, _ database.DBTX, subscription string, at eventstore.Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.positions[subscription] = at
	return nil
}

var (
	_ CheckpointStore = (*PostgresCheckpoints)(nil)
	_ CheckpointStore = (*MemoryCheckpoints)(nil)
)
