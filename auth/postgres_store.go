package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresUserStore struct {
	pool *pgxpool.Pool
}

func NewPostgresUserStore(pool *pgxpool.Pool) *PostgresUserStore {
	return &PostgresUserStore{pool: pool}
}

func (s *PostgresUserStore) Create(user *User) error {
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, $3, $4)`,
		user.ID, user.Email, user.PasswordHash, user.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrEmailTaken
		}
		return fmt.Errorf("inserting user: %w", err)
	}
	return nil
}

func (s *PostgresUserStore) FindByEmail(email string) (*User, error) {
	var user User
	err := s.pool.QueryRow(context.Background(),
		`SELECT id, email, password_hash, created_at FROM users WHERE email = $1`, email).
		Scan(&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("querying user by email: %w", err)
	}
	return &user, nil
}
