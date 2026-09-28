-- +goose Up
-- config: the inbound's mihomo listener template (YAML, see internal/proto). The panel
-- fills it from the old per-preset `settings` on first start; `settings` stays so that a
-- rollback to 0.1.x keeps working. The preset list moves to code (own templates included),
-- so the CHECK of 0001 goes; SQLite can only drop it by rebuilding the table.
CREATE TABLE inbounds_new (
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
INSERT INTO inbounds_new (id, name, preset, port, enabled, settings, created_at, updated_at, display_name)
SELECT id, name, preset, port, enabled, settings, created_at, updated_at, display_name FROM inbounds;
DROP TABLE inbounds;
ALTER TABLE inbounds_new RENAME TO inbounds;

-- +goose Down
-- Inbounds of presets 0.1.x does not know cannot come back.
CREATE TABLE inbounds_old (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  preset       TEXT NOT NULL CHECK (preset IN ('vless_reality_vision', 'vless_reality_xhttp', 'hysteria2', 'tuic_v5')),
  port         TEXT NOT NULL,
  enabled      INTEGER NOT NULL DEFAULT 1,
  settings     TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  display_name TEXT NOT NULL DEFAULT ''
);
INSERT INTO inbounds_old (id, name, preset, port, enabled, settings, created_at, updated_at, display_name)
SELECT id, name, preset, port, enabled, settings, created_at, updated_at, display_name FROM inbounds
WHERE preset IN ('vless_reality_vision', 'vless_reality_xhttp', 'hysteria2', 'tuic_v5');
DROP TABLE inbounds;
ALTER TABLE inbounds_old RENAME TO inbounds;
