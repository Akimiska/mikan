-- +goose Up
-- Traffic of each node, counted in the same transaction as the users' (traffic_hourly and
-- traffic_daily): the same bytes, grouped by the node that carried them. The hours are
-- cut with the users' hours, the days with the users' days.
CREATE TABLE node_traffic_hourly (
  node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  hour    BIGINT NOT NULL,
  up      BIGINT NOT NULL DEFAULT 0,
  down    BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, hour)
);
CREATE INDEX node_traffic_hourly_hour ON node_traffic_hourly (hour);

CREATE TABLE node_traffic_daily (
  node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  day     BIGINT NOT NULL,
  up      BIGINT NOT NULL DEFAULT 0,
  down    BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (node_id, day)
);
CREATE INDEX node_traffic_daily_day ON node_traffic_daily (day);

-- Since the node counted, for the metrics' counters: they only grow, which the rows
-- above (cut when old) cannot promise.
ALTER TABLE nodes ADD COLUMN total_up BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN total_down BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE nodes DROP COLUMN total_down;
ALTER TABLE nodes DROP COLUMN total_up;
DROP TABLE node_traffic_daily;
DROP TABLE node_traffic_hourly;
