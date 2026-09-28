-- +goose Up
-- Nodes: id 1 is the panel's own node behind the unix socket; a remote node has the
-- host:port of its Node API in `address` and its pinned certificate in `cert_sha256`
-- (see internal/nodetls). `public_host`/`domain` are what clients connect to; for the
-- panel's own node they stay empty and the panel settings apply.
CREATE TABLE nodes (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL DEFAULT '',
  address     TEXT NOT NULL DEFAULT '',
  public_host TEXT NOT NULL DEFAULT '',
  domain      TEXT NOT NULL DEFAULT '',
  cert_sha256 TEXT NOT NULL DEFAULT '',
  enabled     INTEGER NOT NULL DEFAULT 1,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
INSERT INTO nodes (id, created_at, updated_at) VALUES (1, CAST(strftime('%s', 'now') AS INTEGER), CAST(strftime('%s', 'now') AS INTEGER));

-- Inbounds belong to a node; names are listener names, unique per node only.
CREATE TABLE inbounds_new (
  id           INTEGER PRIMARY KEY,
  node_id      INTEGER NOT NULL DEFAULT 1 REFERENCES nodes(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  preset       TEXT NOT NULL,
  port         TEXT NOT NULL,
  enabled      INTEGER NOT NULL DEFAULT 1,
  settings     TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  config       TEXT NOT NULL DEFAULT '',
  UNIQUE (node_id, name)
);
INSERT INTO inbounds_new (id, node_id, name, preset, port, enabled, settings, created_at, updated_at, display_name, config)
SELECT id, 1, name, preset, port, enabled, settings, created_at, updated_at, display_name, config FROM inbounds;
DROP TABLE inbounds;
ALTER TABLE inbounds_new RENAME TO inbounds;

-- Counter positions and state revisions are kept per node.
UPDATE node_state SET key = key || '/1' WHERE key IN ('counters_epoch', 'counters_seq', 'revision');

-- +goose Down
-- Remote nodes and their inbounds cannot come back to a single-node panel.
DELETE FROM node_state WHERE key LIKE '%/%' AND key NOT LIKE '%/1';
UPDATE node_state SET key = substr(key, 1, length(key) - 2) WHERE key IN ('counters_epoch/1', 'counters_seq/1', 'revision/1');
CREATE TABLE inbounds_old (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  preset       TEXT NOT NULL,
  port         TEXT NOT NULL,
  enabled      INTEGER NOT NULL DEFAULT 1,
  settings     TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  config       TEXT NOT NULL DEFAULT ''
);
INSERT INTO inbounds_old (id, name, preset, port, enabled, settings, created_at, updated_at, display_name, config)
SELECT id, name, preset, port, enabled, settings, created_at, updated_at, display_name, config FROM inbounds WHERE node_id = 1;
DROP TABLE inbounds;
ALTER TABLE inbounds_old RENAME TO inbounds;
DROP TABLE nodes;
