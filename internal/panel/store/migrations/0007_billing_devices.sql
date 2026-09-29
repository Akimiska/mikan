-- +goose Up
-- Billing day: the term ends on this day of the month (1–31; a shorter month ends it on
-- its last day). "+1 month" moves the expiry to the next one, and the monthly traffic
-- reset (reset_strategy month_start) happens on it instead of the 1st.
ALTER TABLE tariffs ADD COLUMN billing_day INTEGER;
ALTER TABLE users ADD COLUMN billing_day INTEGER;

-- Devices bound to a subscription (see domain.Devices). A device that sends its hardware
-- id (x-hwid) when it fetches the subscription gets a slot of its own: unbinding burns
-- that slot and the device is cut off at once. Apps without an id share the user's own
-- slot (users.slot_id) as a single device, hwid = ''.
CREATE TABLE bound_devices (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  hwid       TEXT NOT NULL,
  slot_id    INTEGER NOT NULL REFERENCES slots(id),
  os         TEXT NOT NULL DEFAULT '',
  os_version TEXT NOT NULL DEFAULT '',
  model      TEXT NOT NULL DEFAULT '',
  app        TEXT NOT NULL DEFAULT '',
  last_ip    TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  UNIQUE (user_id, hwid)
);
CREATE INDEX bound_devices_slot ON bound_devices(slot_id);

-- When the subscriber last unbound a device on the subscription page: once a day, or a
-- reseller would rotate buyers through the places.
ALTER TABLE users ADD COLUMN unbound_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN unbound_at;
DROP TABLE bound_devices;
ALTER TABLE users DROP COLUMN billing_day;
ALTER TABLE tariffs DROP COLUMN billing_day;
