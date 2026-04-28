-- +migrate Up
CREATE TABLE debts_view (
    id         UUID        PRIMARY KEY,
    owner_id   UUID        NOT NULL,
    wallet_id  UUID,
    amount     INTEGER     NOT NULL,
    currency   TEXT        NOT NULL,
    direction  TEXT        NOT NULL,
    reason     TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL DEFAULT 'created',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_debts_view_owner_id ON debts_view (owner_id);

-- +migrate Down
DROP TABLE IF EXISTS debts_view;
