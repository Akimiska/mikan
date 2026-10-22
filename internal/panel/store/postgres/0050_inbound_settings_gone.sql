-- +goose Up
-- The per-preset settings of mikan ≤ 0.1.2: every inbound carries a template (config) since,
-- and no panel this old can be updated to this one directly (it goes through 0.5.0.0).
ALTER TABLE inbounds DROP COLUMN settings;

-- +goose Down
ALTER TABLE inbounds ADD COLUMN settings TEXT NOT NULL DEFAULT '{}';
