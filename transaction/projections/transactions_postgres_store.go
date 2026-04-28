package projections

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresTransactionStore struct {
	pool *pgxpool.Pool
}

func NewPostgresTransactionStore(pool *pgxpool.Pool) *PostgresTransactionStore {
	return &PostgresTransactionStore{pool: pool}
}

func (s *PostgresTransactionStore) Insert(ctx context.Context, view *TransactionView) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO transactions_view (transaction_id, user_id, from_wallet_id, to_wallet_id, amount, currency, reason, status, created_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (transaction_id) DO NOTHING`,
		view.TransactionID, view.UserID, view.FromWalletID, view.ToWalletID,
		view.Amount, view.Currency, view.Reason, view.Status,
		view.CreatedAt, view.CompletedAt,
	)
	return err
}

func (s *PostgresTransactionStore) UpdateStatus(ctx context.Context, transactionID string, status string, completedAt *time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE transactions_view SET status = $2, completed_at = $3
		WHERE transaction_id = $1`,
		transactionID, status, completedAt,
	)
	return err
}

func (s *PostgresTransactionStore) ListByUserID(ctx context.Context, userID string) ([]TransactionView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT transaction_id, user_id, from_wallet_id, to_wallet_id, amount, currency, reason, status, created_at, completed_at
		FROM transactions_view
		WHERE user_id = $1
		ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TransactionView
	for rows.Next() {
		var t TransactionView
		if err := rows.Scan(&t.TransactionID, &t.UserID, &t.FromWalletID, &t.ToWalletID,
			&t.Amount, &t.Currency, &t.Reason, &t.Status,
			&t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}
