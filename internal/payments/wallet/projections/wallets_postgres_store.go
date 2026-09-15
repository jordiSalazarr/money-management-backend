package projections

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"

	"github.com/jordisalazar/money-management/internal/shared/database"
)

type PostgresWalletStore struct {
	db database.DBTX
}

func NewPostgresWalletStore(db database.DBTX) *PostgresWalletStore {
	return &PostgresWalletStore{db: db}
}

func (s *PostgresWalletStore) Upsert(ctx context.Context, view *WalletView) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO wallets_view (id, user_id, name, description, current_balance, currency, category, created_at, last_movement_at, disabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			current_balance  = EXCLUDED.current_balance,
			last_movement_at = EXCLUDED.last_movement_at,
			disabled         = EXCLUDED.disabled`,
		view.ID, view.UserID, view.Name, view.Description,
		view.CurrentBalance, view.Currency, view.Category,
		view.CreatedAt, view.LastMovementAt, view.Disabled,
	)
	return err
}

func (s *PostgresWalletStore) ListByUserID(ctx context.Context, userID string) ([]WalletView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, name, description, current_balance, currency, category, created_at, last_movement_at, disabled
		FROM wallets_view
		WHERE user_id = $1
		ORDER BY disabled, created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []WalletView
	for rows.Next() {
		var w WalletView
		if err := rows.Scan(&w.ID, &w.UserID, &w.Name, &w.Description,
			&w.CurrentBalance, &w.Currency, &w.Category,
			&w.CreatedAt, &w.LastMovementAt, &w.Disabled); err != nil {
			return nil, err
		}
		result = append(result, w)
	}
	return result, rows.Err()
}

func (s *PostgresWalletStore) Get(ctx context.Context, id string) (*WalletView, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, user_id, name, description, current_balance, currency,
		       category, created_at, last_movement_at, disabled
		FROM wallets_view WHERE id = $1`, id)

	var v WalletView
	err := row.Scan(&v.ID, &v.UserID, &v.Name, &v.Description, &v.CurrentBalance,
		&v.Currency, &v.Category, &v.CreatedAt, &v.LastMovementAt, &v.Disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading wallet %s: %w", id, err)
	}
	return &v, nil
}
