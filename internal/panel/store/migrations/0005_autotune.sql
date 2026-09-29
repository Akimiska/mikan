-- +goose Up
-- Automatic moves (internal/panel/autotune): the admin can keep an inbound out of the
-- port move or the REALITY target replacement; the global switches live in settings.
ALTER TABLE inbounds ADD COLUMN auto_port INTEGER NOT NULL DEFAULT 1;
ALTER TABLE inbounds ADD COLUMN auto_sni INTEGER NOT NULL DEFAULT 1;

-- When the user last fetched the subscription: the block detector only trusts devices
-- whose profile already has an inbound's current port and target.
ALTER TABLE users ADD COLUMN sub_fetched_at INTEGER NOT NULL DEFAULT 0;

-- Automatic changes of an inbound: the last one shows on its card; old ports of a node
-- are not picked again for a while.
CREATE TABLE inbound_events (
  id         INTEGER PRIMARY KEY,
  inbound_id INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
  node_id    INTEGER NOT NULL,
  kind       TEXT NOT NULL,             -- port | sni
  network    TEXT NOT NULL DEFAULT '',  -- tcp | udp, for kind = port
  old_value  TEXT NOT NULL,
  new_value  TEXT NOT NULL,
  reason     TEXT NOT NULL,             -- blocked | target_down | still_blocked
  created_at INTEGER NOT NULL
);
CREATE INDEX inbound_events_inbound ON inbound_events(inbound_id, id);

-- +goose Down
DROP TABLE inbound_events;
ALTER TABLE users DROP COLUMN sub_fetched_at;
ALTER TABLE inbounds DROP COLUMN auto_sni;
ALTER TABLE inbounds DROP COLUMN auto_port;
