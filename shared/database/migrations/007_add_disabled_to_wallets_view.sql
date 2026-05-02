-- +migrate Up
ALTER TABLE wallets_view ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +migrate Down
ALTER TABLE wallets_view DROP COLUMN disabled;
