-- +goose Up
-- A node may share its TCP 443 between inbounds by the TLS name clients ask for (the SNI
-- router): the inbounds marked for it listen on loopback behind it, clients connect to 443.
ALTER TABLE nodes ADD COLUMN sni_router BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE nodes DROP COLUMN sni_router;
