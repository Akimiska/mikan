-- +goose Up
-- Where users go, when the admin turned it on: the bytes of each subscription by site and
-- day (UTC), summed on the nodes. No times within the day, no addresses of the clients.
CREATE TABLE user_sites (
  day     BIGINT NOT NULL, -- unix seconds of the day's start, UTC
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site    TEXT NOT NULL,
  up      BIGINT NOT NULL DEFAULT 0,
  down    BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, day, site)
);
CREATE INDEX user_sites_day ON user_sites (day);

-- +goose Down
DROP TABLE user_sites;
