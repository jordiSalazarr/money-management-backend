package projections

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresDebtStore struct {
	pool *pgxpool.Pool
}

func NewPostgresDebtStore(pool *pgxpool.Pool) *PostgresDebtStore {
	return &PostgresDebtStore{pool: pool}
}

func (s *PostgresDebtStore) Upsert(ctx context.Context, view *DebtView) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO debts_view (id, owner_id, wallet_id, amount, currency, direction, reason, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			wallet_id  = EXCLUDED.wallet_id,
			status     = EXCLUDED.status`,
		view.ID, view.OwnerID, view.WalletID,
		view.Amount, view.Currency, view.Direction,
		view.Reason, view.Status, view.CreatedAt,
	)
	return err
}

func (s *PostgresDebtStore) List(ctx context.Context, filter DebtFilter) ([]DebtView, error) {
	query := `SELECT id, owner_id, wallet_id, amount, currency, direction, reason, status, created_at
		FROM debts_view WHERE owner_id = $1`
	args := []any{filter.OwnerID}
	argIdx := 2

	if filter.WalletID != nil {
		query += fmt.Sprintf(" AND wallet_id = $%d", argIdx)
		args = append(args, *filter.WalletID)
		argIdx++
	}
	if filter.Direction != nil {
		query += fmt.Sprintf(" AND direction = $%d", argIdx)
		args = append(args, *filter.Direction)
		argIdx++
	}
	if filter.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, *filter.Status)
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DebtView
	for rows.Next() {
		var d DebtView
		if err := rows.Scan(&d.ID, &d.OwnerID, &d.WalletID,
			&d.Amount, &d.Currency, &d.Direction,
			&d.Reason, &d.Status, &d.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
