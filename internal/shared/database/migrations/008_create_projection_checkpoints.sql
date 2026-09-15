-- projection_checkpoints: how far each projection has read the global log.
--
-- One row per subscription, not one row for the whole projector. That split is
-- the point (ADR 0003): rebuilding a single read model becomes "truncate its
-- table, delete its row, restart", and it catches up alone while the others
-- keep serving. With a shared position, changing one upcaster would mean
-- replaying everything, and one failing projection would stall the rest.
--
-- The checkpoint is written in the same transaction as the rows it accounts
-- for. That is what makes a projection effectively exactly-once: either the
-- rows and the position both land, or neither does. It is also why
-- Subscriber.Handle returns an error — a projection that could not write must
-- be able to abort the transaction rather than let the position advance past
-- work it never did.
-- +migrate Up
CREATE TABLE IF NOT EXISTS projection_checkpoints (
    -- Matches the projection's registered name, e.g. "movements_projector".
    subscription_name TEXT        PRIMARY KEY,

    -- KurrentDB's $all position. Stored as NUMERIC because the positions are
    -- uint64 and Postgres BIGINT is signed — a large enough log would overflow
    -- it, and a silently negative checkpoint is not a failure mode worth
    -- inviting.
    commit_position   NUMERIC(20) NOT NULL,
    prepare_position  NUMERIC(20) NOT NULL,

    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +migrate Down
DROP TABLE IF EXISTS projection_checkpoints;
