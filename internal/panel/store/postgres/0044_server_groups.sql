-- +goose Up
-- Server groups (the traffic pools of issue #6, as the panel calls them now): a group is in
-- a tariff and in a subscription in one of four ways, its mode:
--   main       in, its traffic counts to the main traffic;
--   own        in, with a limit of its own (traffic_limit), apart from the main traffic;
--   unlimited  in, without a limit, apart from the main traffic;
--   closed     not in: left out of the subscription, the nodes turn the user away.
-- A group a tariff or a subscription has no row for is in the main traffic. Until now an
-- unlisted pool was open without a limit, so that meaning is written down for every pool
-- that exists: nothing anyone has changes.
ALTER TABLE tariff_pools ADD COLUMN mode TEXT NOT NULL DEFAULT 'main' CHECK (mode IN ('main', 'own', 'unlimited', 'closed'));
UPDATE tariff_pools SET mode = CASE WHEN excluded THEN 'closed' ELSE 'own' END;
ALTER TABLE tariff_pools ALTER COLUMN traffic_limit DROP NOT NULL;
UPDATE tariff_pools SET traffic_limit = NULL WHERE mode <> 'own';
INSERT INTO tariff_pools (tariff_id, pool_id, traffic_limit, mode)
SELECT t.id, p.id, NULL, 'unlimited' FROM tariffs t CROSS JOIN traffic_pools p
ON CONFLICT (tariff_id, pool_id) DO NOTHING;
ALTER TABLE tariff_pools DROP COLUMN excluded;
ALTER TABLE tariff_pools ADD CONSTRAINT tariff_pools_own_limit CHECK ((mode = 'own') = (traffic_limit IS NOT NULL));

ALTER TABLE user_pools ADD COLUMN mode TEXT NOT NULL DEFAULT 'main' CHECK (mode IN ('main', 'own', 'unlimited', 'closed'));
UPDATE user_pools SET mode = CASE WHEN excluded THEN 'closed' WHEN traffic_limit IS NOT NULL THEN 'own' ELSE 'unlimited' END;
UPDATE user_pools SET traffic_limit = NULL WHERE mode <> 'own';
INSERT INTO user_pools (user_id, pool_id, mode)
SELECT u.id, p.id, 'unlimited' FROM users u CROSS JOIN traffic_pools p
ON CONFLICT (user_id, pool_id) DO NOTHING;
ALTER TABLE user_pools DROP COLUMN excluded;
ALTER TABLE user_pools ADD CONSTRAINT user_pools_own_limit CHECK ((mode = 'own') = (traffic_limit IS NOT NULL));

-- The tariffs a traffic package is sold to; none: every tariff it fits.
CREATE TABLE package_tariffs (
  package_id BIGINT NOT NULL REFERENCES traffic_packages(id) ON DELETE CASCADE,
  tariff_id  BIGINT NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
  PRIMARY KEY (package_id, tariff_id)
);

-- The packages a tariff gives with every payment for it, a purchase or a renewal: the
-- buyer gets each one as a grant of that payment.
CREATE TABLE tariff_gifts (
  tariff_id  BIGINT NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
  package_id BIGINT NOT NULL REFERENCES traffic_packages(id) ON DELETE CASCADE,
  PRIMARY KEY (tariff_id, package_id)
);

-- A payment for a tariff makes a grant of each gift: one grant per payment and package.
ALTER TABLE traffic_grants DROP CONSTRAINT traffic_grants_source_check;
ALTER TABLE traffic_grants ADD CONSTRAINT traffic_grants_source_check CHECK (source IN ('purchase', 'admin', 'gift'));
ALTER TABLE traffic_grants DROP CONSTRAINT traffic_grants_payment_id_key;
CREATE UNIQUE INDEX traffic_grants_payment_package ON traffic_grants (payment_id, package_id) WHERE payment_id IS NOT NULL;

-- +goose Down
-- A group in the main traffic had no way to be said before: it goes back open without a
-- limit, as an unlisted pool was. The gifts go with their grants.
DROP INDEX traffic_grants_payment_package;
DELETE FROM traffic_grants WHERE source = 'gift';
ALTER TABLE traffic_grants ADD CONSTRAINT traffic_grants_payment_id_key UNIQUE (payment_id);
ALTER TABLE traffic_grants DROP CONSTRAINT traffic_grants_source_check;
ALTER TABLE traffic_grants ADD CONSTRAINT traffic_grants_source_check CHECK (source IN ('purchase', 'admin'));
DROP TABLE tariff_gifts;
DROP TABLE package_tariffs;

ALTER TABLE user_pools DROP CONSTRAINT user_pools_own_limit;
ALTER TABLE user_pools ADD COLUMN excluded BOOLEAN NOT NULL DEFAULT false;
UPDATE user_pools SET excluded = (mode = 'closed');
ALTER TABLE user_pools DROP COLUMN mode;

ALTER TABLE tariff_pools DROP CONSTRAINT tariff_pools_own_limit;
ALTER TABLE tariff_pools ADD COLUMN excluded BOOLEAN NOT NULL DEFAULT false;
UPDATE tariff_pools SET excluded = (mode = 'closed');
DELETE FROM tariff_pools WHERE mode IN ('main', 'unlimited');
UPDATE tariff_pools SET traffic_limit = 0 WHERE traffic_limit IS NULL;
ALTER TABLE tariff_pools ALTER COLUMN traffic_limit SET NOT NULL;
ALTER TABLE tariff_pools DROP COLUMN mode;
