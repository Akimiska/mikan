-- +goose Up
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE admins (
  id             INTEGER PRIMARY KEY,
  username       TEXT NOT NULL UNIQUE,
  password_hash  TEXT NOT NULL,
  totp_secret    TEXT,
  recovery_codes TEXT,
  created_at     INTEGER NOT NULL,
  last_login_at  INTEGER
);

CREATE TABLE sessions (
  id_hash      TEXT PRIMARY KEY,
  admin_id     INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
  csrf_token   TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  ip           TEXT NOT NULL,
  user_agent   TEXT NOT NULL
);
CREATE INDEX sessions_admin ON sessions(admin_id);

CREATE TABLE audit_log (
  id          INTEGER PRIMARY KEY,
  ts          INTEGER NOT NULL,
  admin_id    INTEGER,
  action      TEXT NOT NULL,
  target_type TEXT,
  target_id   TEXT,
  ip          TEXT,
  details     TEXT
);

CREATE TABLE tariffs (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL,
  traffic_limit  INTEGER,
  duration_days  INTEGER NOT NULL,
  device_limit   INTEGER,
  reset_strategy TEXT NOT NULL DEFAULT 'none' CHECK (reset_strategy IN ('none', 'month_start', 'period')),
  price_label    TEXT NOT NULL DEFAULT '',
  sort           INTEGER NOT NULL DEFAULT 0,
  archived       INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL
);

CREATE TABLE slots (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  uuid       TEXT NOT NULL UNIQUE,
  secret     TEXT NOT NULL,
  state      TEXT NOT NULL CHECK (state IN ('free', 'assigned', 'burned')),
  created_at INTEGER NOT NULL,
  burned_at  INTEGER
);
CREATE INDEX slots_state ON slots(state);

CREATE TABLE users (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL,
  contact        TEXT NOT NULL DEFAULT '',
  note           TEXT NOT NULL DEFAULT '',
  tags           TEXT NOT NULL DEFAULT '[]',
  status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  tariff_id      INTEGER REFERENCES tariffs(id) ON DELETE SET NULL,
  traffic_limit  INTEGER,
  device_limit   INTEGER,
  reset_strategy TEXT NOT NULL DEFAULT 'none' CHECK (reset_strategy IN ('none', 'month_start', 'period')),
  period_days    INTEGER NOT NULL DEFAULT 0,
  period_start   INTEGER NOT NULL,
  used_up        INTEGER NOT NULL DEFAULT 0,
  used_down      INTEGER NOT NULL DEFAULT 0,
  total_up       INTEGER NOT NULL DEFAULT 0,
  total_down     INTEGER NOT NULL DEFAULT 0,
  expires_at     INTEGER,
  inbounds       TEXT,
  sub_token      TEXT NOT NULL UNIQUE,
  slot_id        INTEGER UNIQUE REFERENCES slots(id),
  online_at      INTEGER,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX users_expires ON users(expires_at);
CREATE INDEX users_status ON users(status);

CREATE TABLE inbounds (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  preset     TEXT NOT NULL CHECK (preset IN ('vless_reality_vision', 'vless_reality_xhttp', 'hysteria2', 'tuic_v5')),
  port       TEXT NOT NULL,
  enabled    INTEGER NOT NULL DEFAULT 1,
  settings   TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE traffic_hourly (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  hour    INTEGER NOT NULL,
  up      INTEGER NOT NULL DEFAULT 0,
  down    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, hour)
) WITHOUT ROWID;

CREATE TABLE traffic_daily (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day     INTEGER NOT NULL,
  up      INTEGER NOT NULL DEFAULT 0,
  down    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, day)
) WITHOUT ROWID;

CREATE TABLE devices (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ip         TEXT NOT NULL,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  client     TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, ip)
) WITHOUT ROWID;

CREATE TABLE node_state (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- +goose Down
DROP TABLE node_state;
DROP TABLE devices;
DROP TABLE traffic_daily;
DROP TABLE traffic_hourly;
DROP TABLE inbounds;
DROP TABLE users;
DROP TABLE slots;
DROP TABLE tariffs;
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE admins;
DROP TABLE settings;
