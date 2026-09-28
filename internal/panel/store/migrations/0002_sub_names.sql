-- +goose Up
-- Name shown in the subscription instead of the preset's default, e.g. "🇳🇱 Нидерланды".
ALTER TABLE inbounds ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE inbounds DROP COLUMN display_name;
