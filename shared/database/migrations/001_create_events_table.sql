-- +migrate Up
CREATE TABLE events (
    id             BIGSERIAL   PRIMARY KEY,
    event_id       UUID        NOT NULL UNIQUE,
    aggregate_id   UUID        NOT NULL,
    aggregate_type TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    version        INTEGER     NOT NULL,
    correlation_id TEXT        NOT NULL DEFAULT '',
    payload        JSONB       NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (aggregate_id, version)
);

CREATE INDEX idx_events_aggregate_id ON events (aggregate_id, version);

-- +migrate Down
DROP TABLE IF EXISTS events;
