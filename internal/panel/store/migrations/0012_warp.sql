-- +goose Up
-- Cloudflare WARP as a way out of a node (GitHub issue #4). An inbound leaves directly
-- or through its node's WARP; listed domains and networks go through WARP for all.
ALTER TABLE inbounds ADD COLUMN outbound TEXT NOT NULL DEFAULT 'direct' CHECK (outbound IN ('direct', 'warp'));

CREATE TABLE node_warp (
  node_id         INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  enabled         INTEGER NOT NULL DEFAULT 1,
  source          TEXT NOT NULL CHECK (source IN ('register', 'import')),
  private_key     TEXT NOT NULL,                -- never leaves the panel but to its node
  peer_public_key TEXT NOT NULL,
  endpoint        TEXT NOT NULL,                -- host:port
  ipv4            TEXT NOT NULL,
  ipv6            TEXT NOT NULL DEFAULT '',
  reserved        TEXT NOT NULL DEFAULT '',     -- base64 of WARP's 3-byte client id
  mtu             INTEGER NOT NULL DEFAULT 1280,
  account_id      TEXT NOT NULL DEFAULT '',     -- the Cloudflare registration, for a WARP+ key
  account_token   TEXT NOT NULL DEFAULT '',
  plus            INTEGER NOT NULL DEFAULT 0,
  routes          TEXT NOT NULL DEFAULT '[]',   -- JSON: domain suffixes and networks through WARP
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

-- +goose Down
DROP TABLE node_warp;
ALTER TABLE inbounds DROP COLUMN outbound;
