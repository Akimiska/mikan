-- +goose Up
-- When each slot last got through to each inbound. The block detector counts a device as
-- cut off only from an inbound it has reached before: an app that does not speak the
-- protocol, or a profile without the inbound, never reaches it and proves nothing.
CREATE TABLE inbound_reach (
  slot       TEXT NOT NULL,
  inbound_id INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
  at         INTEGER NOT NULL,
  PRIMARY KEY (slot, inbound_id)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE inbound_reach;
