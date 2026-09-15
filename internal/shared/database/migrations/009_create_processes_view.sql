-- processes_view: one row per cross-aggregate process, whatever kind, with
-- where it got to and why.
--
-- It replaces transactions_view, which only ever knew about transfers, was
-- written by its projector, and was read by nobody.
--
-- Two consumers: the API, so a 202 from POST /transfers is pollable, and the
-- sweeper, which queries the in-flight rows to find processes that have
-- stopped making progress.
-- +migrate Up
CREATE TABLE IF NOT EXISTS processes_view (
    -- transfer | occurrence | debt_settlement
    kind           TEXT        NOT NULL,
    id             TEXT        NOT NULL,
    user_id        TEXT        NOT NULL,

    -- Non-terminal: requested, debited, due, settling.
    -- Terminal:     completed, failed, stuck, executed, skipped, settled.
    status         TEXT        NOT NULL,
    -- snake_case failure code, matching the API's error_code vocabulary.
    -- Null unless the process failed, was skipped, or is stuck.
    reason         TEXT,

    amount         BIGINT      NOT NULL DEFAULT 0,
    currency       TEXT        NOT NULL DEFAULT '',
    -- How many times the sweeper has re-driven this process.
    attempts       INT         NOT NULL DEFAULT 0,

    -- Transfers only.
    from_wallet_id TEXT,
    to_wallet_id   TEXT,

    requested_at   TIMESTAMPTZ NOT NULL,
    -- Null while in flight. The sweeper keys off this.
    settled_at     TIMESTAMPTZ,

    PRIMARY KEY (kind, id)
);

-- The sweeper's query: in-flight rows older than its threshold.
CREATE INDEX IF NOT EXISTS processes_view_in_flight_idx
    ON processes_view (requested_at)
    WHERE settled_at IS NULL;

CREATE INDEX IF NOT EXISTS processes_view_user_idx
    ON processes_view (user_id, requested_at DESC);

DROP TABLE IF EXISTS transactions_view;

-- +migrate Down
DROP TABLE IF EXISTS processes_view;
