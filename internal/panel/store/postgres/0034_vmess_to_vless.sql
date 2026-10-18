-- +goose Up
-- The nodes' core (Citrus) has no VMess. An inbound on VMess becomes VLESS on the same
-- transport and security: both always run on REALITY or the node certificate, the users
-- keep their UUIDs, and clients get the new link with their next subscription update.
-- Templates are stored with their type first (proto.Marshal).
UPDATE inbounds SET config = regexp_replace(config, '^type: vmess\n', E'type: vless\n')
WHERE config ~ '^type: vmess\n';

-- +goose Down
-- Which inbounds were VMess is not kept: a rollback leaves them on VLESS.
SELECT 1;
