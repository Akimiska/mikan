-- +goose Up
-- A node may answer plain HTTP on port 80 with 301 to https: it holds the port for good,
-- answering the panel's ACME challenges there as before.
ALTER TABLE nodes ADD COLUMN http_redirect BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE nodes DROP COLUMN http_redirect;
