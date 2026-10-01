-- +goose Up
-- The tables below are keyed by (user_id, time) and read or pruned by time alone, which
-- the primary key cannot serve: the dashboard, the top users and every prune scanned the
-- whole table (a year of 10 000 users is millions of rows). One index per such filter.
CREATE INDEX traffic_hourly_hour ON traffic_hourly(hour);
CREATE INDEX traffic_daily_day ON traffic_daily(day);
CREATE INDEX devices_last_seen ON devices(last_seen);
CREATE INDEX audit_log_ts ON audit_log(ts);

-- devices.client was never written: the user agent is not known where devices are noted.
ALTER TABLE devices DROP COLUMN client;

-- +goose Down
ALTER TABLE devices ADD COLUMN client TEXT NOT NULL DEFAULT '';
DROP INDEX audit_log_ts;
DROP INDEX devices_last_seen;
DROP INDEX traffic_daily_day;
DROP INDEX traffic_hourly_hour;
