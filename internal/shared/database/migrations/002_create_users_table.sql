-- +migrate Up
CREATE TABLE users (
    id            UUID        PRIMARY KEY,
    email         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users (email);

-- +migrate Down
DROP TABLE IF EXISTS users;
