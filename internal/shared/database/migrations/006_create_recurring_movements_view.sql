-- +migrate Up
CREATE TABLE recurring_movements_view (
    id             UUID        PRIMARY KEY,
    wallet_id      UUID        NOT NULL,
    user_id        UUID        NOT NULL,
    type           TEXT        NOT NULL,
    amount         INTEGER     NOT NULL,
    currency       TEXT        NOT NULL,
    frequency      TEXT        NOT NULL,
    reason         TEXT        NOT NULL DEFAULT '',
    start_date     DATE        NOT NULL,
    end_date       DATE,
    next_due_date  DATE        NOT NULL,
    active         BOOLEAN     NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_recurring_movements_due ON recurring_movements_view (next_due_date) WHERE active = true;
CREATE INDEX idx_recurring_movements_wallet ON recurring_movements_view (wallet_id);

-- +migrate Down
DROP TABLE IF EXISTS recurring_movements_view;
