-- +goose Up
-- Addons installed from the registry of community addons (internal/panel/addons,
-- community.go): the repository the snapshot named, and the author's key the release was
-- signed with when the admin approved it ('' for an unsigned one). An update signed with
-- another key waits for the admin to take that key.
CREATE TABLE addon_community (
  addon_id TEXT PRIMARY KEY,
  repo     TEXT NOT NULL,
  key      TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE addon_community;
