-- +goose Up
-- Promo tables ship in the baseline until PostgreSQL migrations are released.

-- +goose Down
-- The baseline owns promo tables; this migration must not drop them.
