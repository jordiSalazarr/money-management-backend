package projections

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"

	"github.com/jordisalazar/money-management/internal/shared/database"
)

type PostgresRecurringMovementStore struct {
	db database.DBTX
}

func NewPostgresRecurringMovementStore(db database.DBTX) *PostgresRecurringMovementStore {
	return &PostgresRecurringMovementStore{db: db}
}

func (s *PostgresRecurringMovementStore) Upsert(ctx context.Context, view *RecurringMovementView) error {
	_, err := s.db.Exec(ctx, `
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

	rows, err := s.db.Query(ctx, query, args...)
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

func (s *PostgresRecurringMovementStore) Get(ctx context.Context, id string) (*RecurringMovementView, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, wallet_id, user_id, type, amount, currency, frequency, reason,
		       start_date, end_date, next_due_date, active, created_at
		FROM recurring_movements_view WHERE id = $1`, id)

	var v RecurringMovementView
	err := row.Scan(&v.ID, &v.WalletID, &v.UserID, &v.Type, &v.Amount, &v.Currency,
		&v.Frequency, &v.Reason, &v.StartDate, &v.EndDate, &v.NextDueDate, &v.Active, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading recurring movement %s: %w", id, err)
	}
	return &v, nil
}
