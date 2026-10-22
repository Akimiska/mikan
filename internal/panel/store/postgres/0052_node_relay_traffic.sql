-- +goose Up
-- What a node's relay carried for the other nodes' cascades, by the hour and the day: the
-- node's load apart from up and down, which hold its users' bytes and add up to the users'
-- statistics. The source node counted the same bytes for its users, so no user, quota or
-- total of the panel takes these.
ALTER TABLE node_traffic_hourly ADD COLUMN relay_up BIGINT NOT NULL DEFAULT 0;
ALTER TABLE node_traffic_hourly ADD COLUMN relay_down BIGINT NOT NULL DEFAULT 0;
ALTER TABLE node_traffic_daily ADD COLUMN relay_up BIGINT NOT NULL DEFAULT 0;
ALTER TABLE node_traffic_daily ADD COLUMN relay_down BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE node_traffic_daily DROP COLUMN relay_down;
ALTER TABLE node_traffic_daily DROP COLUMN relay_up;
ALTER TABLE node_traffic_hourly DROP COLUMN relay_down;
ALTER TABLE node_traffic_hourly DROP COLUMN relay_up;
