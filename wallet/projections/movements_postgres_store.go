package projections

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresMovementStore struct {
	pool *pgxpool.Pool
}

func NewPostgresMovementStore(pool *pgxpool.Pool) *PostgresMovementStore {
	return &PostgresMovementStore{pool: pool}
}

func (s *PostgresMovementStore) Insert(ctx context.Context, view *MovementView) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO movements (id, wallet_id, type, amount, currency, reason, category, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING`,
		view.ID, view.WalletID, view.Type, view.Amount,
		view.Currency, view.Reason, view.Category, view.OccurredAt,
	)
	return err
}

func (s *PostgresMovementStore) ListByWalletID(ctx context.Context, walletID string) ([]MovementView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, wallet_id, type, amount, currency, reason, category, occurred_at
		FROM movements
		WHERE wallet_id = $1
		ORDER BY occurred_at`, walletID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []MovementView
	for rows.Next() {
		var m MovementView
		if err := rows.Scan(&m.ID, &m.WalletID, &m.Type, &m.Amount,
			&m.Currency, &m.Reason, &m.Category, &m.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
