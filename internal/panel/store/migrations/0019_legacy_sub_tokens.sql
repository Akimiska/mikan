-- +goose Up
-- Kept with the PostgreSQL baseline: the SQLite importer brings a legacy database to the
-- last of these before it copies, and the two schemas must match.
CREATE TABLE legacy_sub_tokens (
  token   TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source  TEXT NOT NULL
) WITHOUT ROWID;
CREATE INDEX legacy_sub_tokens_user ON legacy_sub_tokens (user_id);

-- +goose Down
DROP TABLE legacy_sub_tokens;
