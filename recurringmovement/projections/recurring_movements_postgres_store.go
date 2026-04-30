package projections

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRecurringMovementStore struct {
	pool *pgxpool.Pool
}

func NewPostgresRecurringMovementStore(pool *pgxpool.Pool) *PostgresRecurringMovementStore {
	return &PostgresRecurringMovementStore{pool: pool}
}

func (s *PostgresRecurringMovementStore) Upsert(ctx context.Context, view *RecurringMovementView) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO recurring_movements_view (id, wallet_id, user_id, type, amount, currency, frequency, reason, start_date, end_date, next_due_date, active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			next_due_date = EXCLUDED.next_due_date,
			active        = EXCLUDED.active`,
		view.ID, view.WalletID, view.UserID,
		view.Type, view.Amount, view.Currency,
		view.Frequency, view.Reason, view.StartDate,
		view.EndDate, view.NextDueDate, view.Active,
		view.CreatedAt,
	)
	return err
}

func (s *PostgresRecurringMovementStore) List(ctx context.Context, filter RecurringMovementFilter) ([]RecurringMovementView, error) {
	query := `SELECT id, wallet_id, user_id, type, amount, currency, frequency, reason, start_date, end_date, next_due_date, active, created_at
		FROM recurring_movements_view WHERE 1=1`
	args := []any{}
	argIdx := 1

	if filter.WalletID != "" {
		query += fmt.Sprintf(" AND wallet_id = $%d", argIdx)
		args = append(args, filter.WalletID)
		argIdx++
	}
	if filter.Active != nil {
		query += fmt.Sprintf(" AND active = $%d", argIdx)
		args = append(args, *filter.Active)
		argIdx++
	}

	query += " ORDER BY next_due_date ASC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RecurringMovementView
	for rows.Next() {
		var v RecurringMovementView
		if err := rows.Scan(&v.ID, &v.WalletID, &v.UserID,
			&v.Type, &v.Amount, &v.Currency,
			&v.Frequency, &v.Reason, &v.StartDate,
			&v.EndDate, &v.NextDueDate, &v.Active,
			&v.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
