package projections

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresWalletStore struct {
	pool *pgxpool.Pool
}

func NewPostgresWalletStore(pool *pgxpool.Pool) *PostgresWalletStore {
	return &PostgresWalletStore{pool: pool}
}

func (s *PostgresWalletStore) Upsert(ctx context.Context, view *WalletView) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO wallets_view (id, user_id, name, description, current_balance, currency, category, created_at, last_movement_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			current_balance  = EXCLUDED.current_balance,
			last_movement_at = EXCLUDED.last_movement_at`,
		view.ID, view.UserID, view.Name, view.Description,
		view.CurrentBalance, view.Currency, view.Category,
		view.CreatedAt, view.LastMovementAt,
	)
	return err
}

func (s *PostgresWalletStore) ListByUserID(ctx context.Context, userID string) ([]WalletView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, name, description, current_balance, currency, category, created_at, last_movement_at
		FROM wallets_view
		WHERE user_id = $1
		ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []WalletView
	for rows.Next() {
		var w WalletView
		if err := rows.Scan(&w.ID, &w.UserID, &w.Name, &w.Description,
			&w.CurrentBalance, &w.Currency, &w.Category,
			&w.CreatedAt, &w.LastMovementAt); err != nil {
			return nil, err
		}
		result = append(result, w)
	}
	return result, rows.Err()
}
