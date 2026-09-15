package projections

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jordisalazar/money-management/internal/shared/database"
)

type PostgresProcessStore struct {
	db database.DBTX
}

func NewPostgresProcessStore(db database.DBTX) *PostgresProcessStore {
	return &PostgresProcessStore{db: db}
}

const processColumns = `kind, id, user_id, status, reason, amount, currency,
	attempts, from_wallet_id, to_wallet_id, requested_at, settled_at`

func (s *PostgresProcessStore) Upsert(ctx context.Context, view *ProcessView) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO processes_view (`+processColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (kind, id) DO UPDATE SET
			status     = EXCLUDED.status,
			reason     = EXCLUDED.reason,
			attempts   = EXCLUDED.attempts,
			settled_at = EXCLUDED.settled_at`,
		view.Kind, view.ID, view.UserID, view.Status, view.Reason,
		view.Amount, view.Currency, view.Attempts,
		view.FromWalletID, view.ToWalletID, view.RequestedAt, view.SettledAt,
	)
	return err
}

func (s *PostgresProcessStore) Get(ctx context.Context, kind, id string) (*ProcessView, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+processColumns+` FROM processes_view WHERE kind = $1 AND id = $2`, kind, id)

	view, err := scanProcess(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// A missing row is not an error: the caller distinguishes "no such
		// process" from "the query failed", and conflating them would turn an
		// ordering bug into a silent no-op.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return view, nil
}

// ListInFlightBefore is the sweeper's query: processes that have not reached a
// terminal status and have been waiting longer than the threshold.
func (s *PostgresProcessStore) ListInFlightBefore(ctx context.Context, cutoff time.Time) ([]ProcessView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+processColumns+` FROM processes_view
		WHERE settled_at IS NULL
		  AND status NOT IN ('completed', 'failed', 'stuck', 'executed', 'skipped', 'settled')
		  AND requested_at < $1
		ORDER BY requested_at, id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectProcesses(rows)
}

func (s *PostgresProcessStore) ListByUserID(ctx context.Context, userID string) ([]ProcessView, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+processColumns+` FROM processes_view WHERE user_id = $1 ORDER BY requested_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectProcesses(rows)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanProcess(row scannable) (*ProcessView, error) {
	var v ProcessView
	var reason, fromWallet, toWallet *string
	var settledAt *time.Time

	if err := row.Scan(&v.Kind, &v.ID, &v.UserID, &v.Status, &reason,
		&v.Amount, &v.Currency, &v.Attempts,
		&fromWallet, &toWallet, &v.RequestedAt, &settledAt); err != nil {
		return nil, err
	}

	v.Reason = derefOrEmpty(reason)
	v.FromWalletID = derefOrEmpty(fromWallet)
	v.ToWalletID = derefOrEmpty(toWallet)
	v.SettledAt = settledAt
	return &v, nil
}

func collectProcesses(rows pgx.Rows) ([]ProcessView, error) {
	var out []ProcessView
	for rows.Next() {
		v, err := scanProcess(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
