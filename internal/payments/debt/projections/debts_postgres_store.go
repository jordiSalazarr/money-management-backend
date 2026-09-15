package projections

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"

	"github.com/jordisalazar/money-management/internal/shared/database"
)

type PostgresDebtStore struct {
	db database.DBTX
}

func NewPostgresDebtStore(db database.DBTX) *PostgresDebtStore {
	return &PostgresDebtStore{db: db}
}

func (s *PostgresDebtStore) Upsert(ctx context.Context, view *DebtView) error {
	_, err := s.db.Exec(ctx, `
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

	if filter.WalletID != "" {
		query += fmt.Sprintf(" AND wallet_id = $%d", argIdx)
		args = append(args, filter.WalletID)
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

	rows, err := s.db.Query(ctx, query, args...)
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

func (s *PostgresDebtStore) Get(ctx context.Context, id string) (*DebtView, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, owner_id, wallet_id, amount, currency, direction, reason, status, created_at
		FROM debts_view WHERE id = $1`, id)

	var v DebtView
	err := row.Scan(&v.ID, &v.OwnerID, &v.WalletID, &v.Amount, &v.Currency,
		&v.Direction, &v.Reason, &v.Status, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading debt %s: %w", id, err)
	}
	return &v, nil
}
