-- +goose Up
-- API keys for scripts and integrations: "Authorization: Bearer <key>". Only the key's
-- SHA-256 is kept; the key itself is shown once, when it is created.
CREATE TABLE api_keys (
  id           INTEGER PRIMARY KEY,
  admin_id     INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  prefix       TEXT NOT NULL,        -- the key's first characters, to tell keys apart
  hash         TEXT NOT NULL UNIQUE, -- hex SHA-256 of the key
  scope        TEXT NOT NULL CHECK (scope IN ('read', 'full')),
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER,              -- NULL: until revoked
  last_used_at INTEGER,
  last_ip      TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE api_keys;
