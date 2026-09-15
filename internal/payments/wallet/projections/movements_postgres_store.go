package projections

import (
	"context"

	"github.com/jordisalazar/money-management/internal/shared/database"
)

type PostgresMovementStore struct {
	db database.DBTX
}

func NewPostgresMovementStore(db database.DBTX) *PostgresMovementStore {
	return &PostgresMovementStore{db: db}
}

func (s *PostgresMovementStore) Insert(ctx context.Context, view *MovementView) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO movements (id, wallet_id, type, amount, currency, reason, category, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING`,
		view.ID, view.WalletID, view.Type, view.Amount,
		view.Currency, view.Reason, view.Category, view.OccurredAt,
	)
	return err
}

func (s *PostgresMovementStore) ListByWalletID(ctx context.Context, walletID string) ([]MovementView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, wallet_id, type, amount, currency, reason, category, occurred_at
		FROM movements
		WHERE wallet_id = $1
		ORDER BY occurred_at DESC`, walletID)
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
