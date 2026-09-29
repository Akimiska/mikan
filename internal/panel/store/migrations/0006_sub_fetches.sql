-- +goose Up
-- Subscription fetches per device (user and IP) instead of per user: the block detector
-- trusts a device only when that very device took the profile after an inbound changed.
-- A user's phone updating its profile says nothing about a laptop still on old ports.
CREATE TABLE sub_fetches (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ip         TEXT NOT NULL,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, ip)
) WITHOUT ROWID;
ALTER TABLE users DROP COLUMN sub_fetched_at;

-- +goose Down
ALTER TABLE users ADD COLUMN sub_fetched_at INTEGER NOT NULL DEFAULT 0;
DROP TABLE sub_fetches;
