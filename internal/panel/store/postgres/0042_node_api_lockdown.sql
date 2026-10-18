-- +goose Up
-- A remote node may answer its API only on the panel's addresses: api_allow are networks the
-- admin adds to the ones the panel knows of itself (its public address, and where it reaches
-- the node from).
ALTER TABLE nodes ADD COLUMN api_lockdown BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN api_allow TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE nodes DROP COLUMN api_allow;
ALTER TABLE nodes DROP COLUMN api_lockdown;
