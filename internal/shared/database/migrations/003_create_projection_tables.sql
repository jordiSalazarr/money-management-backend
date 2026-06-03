-- +migrate Up
CREATE TABLE wallets_view (
    id               UUID        PRIMARY KEY,
    user_id          UUID        NOT NULL,
    name             TEXT        NOT NULL,
    description      TEXT        NOT NULL DEFAULT '',
    current_balance  INTEGER     NOT NULL DEFAULT 0,
    currency         TEXT        NOT NULL,
    category         TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL,
    last_movement_at TIMESTAMPTZ
);

CREATE INDEX idx_wallets_view_user_id ON wallets_view (user_id);

CREATE TABLE movements (
    id          UUID        PRIMARY KEY,
    wallet_id   UUID        NOT NULL,
    type        TEXT        NOT NULL,
    amount      INTEGER     NOT NULL,
    currency    TEXT        NOT NULL,
    reason      TEXT        NOT NULL DEFAULT '',
    category    TEXT        NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_movements_wallet_id ON movements (wallet_id, occurred_at);

CREATE TABLE transactions_view (
    transaction_id UUID        PRIMARY KEY,
    user_id        UUID        NOT NULL,
    from_wallet_id UUID        NOT NULL,
    to_wallet_id   UUID        NOT NULL,
    amount         INTEGER     NOT NULL,
    currency       TEXT        NOT NULL,
    reason         TEXT        NOT NULL DEFAULT '',
    status         TEXT        NOT NULL DEFAULT 'CREATED',
    created_at     TIMESTAMPTZ NOT NULL,
    completed_at   TIMESTAMPTZ
);

CREATE INDEX idx_transactions_view_user_id ON transactions_view (user_id);

-- +migrate Down
DROP TABLE IF EXISTS transactions_view;
DROP TABLE IF EXISTS movements;
DROP TABLE IF EXISTS wallets_view;
