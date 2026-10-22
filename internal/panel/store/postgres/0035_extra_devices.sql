-- +goose Up
-- Extra devices bought in the bot and the Mini App: a tariff puts a price on one more place
-- and a cap on how many may be bought; a subscription keeps how many it bought, and until
-- when (NULL: they stay with renewals, which are then priced with them).
ALTER TABLE tariffs ADD COLUMN device_price_stars BIGINT;
ALTER TABLE tariffs ADD COLUMN device_price_rub BIGINT;
ALTER TABLE tariffs ADD COLUMN device_extra_max BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN extra_devices BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN extra_devices_until BIGINT;
ALTER TABLE payments DROP CONSTRAINT payments_kind_check;
ALTER TABLE payments ADD CONSTRAINT payments_kind_check CHECK (kind IN ('new', 'renew', 'package', 'trial', 'device'));

-- +goose Down
ALTER TABLE payments DROP CONSTRAINT payments_kind_check;
ALTER TABLE payments ADD CONSTRAINT payments_kind_check CHECK (kind IN ('new', 'renew', 'package', 'trial')) NOT VALID;
ALTER TABLE users DROP COLUMN extra_devices_until;
ALTER TABLE users DROP COLUMN extra_devices;
ALTER TABLE tariffs DROP COLUMN device_extra_max;
ALTER TABLE tariffs DROP COLUMN device_price_rub;
ALTER TABLE tariffs DROP COLUMN device_price_stars;
