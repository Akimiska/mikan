-- Server groups (traffic_pools): a group of inbounds that a tariff and a subscription hold
-- in one mode each (0044_server_groups.sql): main, own, unlimited or closed.

-- name: ListTrafficPools :many
SELECT * FROM traffic_pools ORDER BY name;

-- name: GetTrafficPool :one
SELECT * FROM traffic_pools WHERE id = $1;

-- name: CreateTrafficPool :one
INSERT INTO traffic_pools (name, created_at) VALUES ($1, $2) RETURNING *;

-- name: RenameTrafficPool :execrows
UPDATE traffic_pools SET name = $1 WHERE id = $2;

-- name: DeleteTrafficPool :execrows
DELETE FROM traffic_pools WHERE id = $1;

-- name: SetInboundPool :exec
UPDATE inbounds SET pool_id = $1 WHERE id = $2;

-- name: SetGroupInbounds :execrows
-- The group's inbounds become exactly these: the ones listed join it (leaving the group
-- they were in), the ones it had and the list leaves out go back to the main servers.
UPDATE inbounds SET pool_id = CASE WHEN id = ANY(sqlc.arg(ids)::bigint[]) THEN sqlc.arg(pool_id)::BIGINT ELSE NULL END
WHERE id = ANY(sqlc.arg(ids)::bigint[]) OR pool_id = sqlc.arg(pool_id)::BIGINT;

-- name: ListTariffPools :many
SELECT * FROM tariff_pools WHERE tariff_id = $1 ORDER BY pool_id;

-- name: ListAllTariffPools :many
SELECT * FROM tariff_pools ORDER BY tariff_id, pool_id;

-- name: ClearTariffPools :exec
DELETE FROM tariff_pools WHERE tariff_id = $1;

-- name: AddTariffPool :exec
INSERT INTO tariff_pools (tariff_id, pool_id, mode, traffic_limit) VALUES ($1, $2, $3, $4);

-- name: ListUserIDsOfTariff :many
-- The users a tariff was given to, for bringing them up to the tariff.
SELECT id FROM users WHERE tariff_id = $1 ORDER BY id;

-- name: CountUsersByTariff :many
-- How many subscriptions each tariff was given to.
SELECT CAST(tariff_id AS BIGINT) AS tariff_id, COUNT(*) AS users FROM users WHERE tariff_id IS NOT NULL GROUP BY tariff_id;

-- name: ListUserPools :many
SELECT * FROM user_pools WHERE user_id = $1 ORDER BY pool_id;

-- name: ListAllUserPools :many
SELECT * FROM user_pools ORDER BY user_id, pool_id;

-- name: ResetUserPoolModes :exec
-- Every group of the user back in the main traffic, before a tariff's modes are written:
-- what the user used stays.
UPDATE user_pools SET mode = 'main', traffic_limit = NULL WHERE user_id = $1;

-- name: SetUserPool :exec
-- How a group is in the subscription; traffic_limit only with the mode own.
INSERT INTO user_pools (user_id, pool_id, mode, traffic_limit) VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, pool_id) DO UPDATE SET mode = excluded.mode, traffic_limit = excluded.traffic_limit;

-- name: UserPoolModes :many
-- The modes of these users' groups, for a traffic batch: a group without a row is in the
-- main traffic.
SELECT user_id, pool_id, mode FROM user_pools
WHERE user_id = ANY(sqlc.arg(user_ids)::bigint[]) AND pool_id = ANY(sqlc.arg(pool_ids)::bigint[]);

-- name: ResetUserPools :exec
-- Pools reset with the main traffic: a new period, a renewal, the admin's reset.
UPDATE user_pools SET used_up = 0, used_down = 0 WHERE user_id = $1;

-- name: PoolUsage :one
-- What a pool still holds that deleting it would destroy (the cascade takes grants and
-- packages with it, and a paid invoice of a package loses the package): traffic users
-- paid for and have left, packages of the catalog, invoices not closed yet.
SELECT
  (SELECT COUNT(*) FROM traffic_grants g
    WHERE g.pool_id = sqlc.arg(pool_id) AND g.remaining > 0 AND (g.expires_at IS NULL OR g.expires_at > CAST(sqlc.arg(now) AS BIGINT))) AS grants,
  (SELECT COUNT(*) FROM traffic_packages k WHERE k.pool_id = sqlc.arg(pool_id) AND k.archived = 0) AS packages,
  (SELECT COUNT(*) FROM payments p JOIN traffic_packages k ON k.id = p.package_id
    WHERE k.pool_id = sqlc.arg(pool_id) AND p.status IN ('pending', 'paid')) AS payments;

-- name: SetTariffUsersPool :exec
-- The users of a tariff follow a group it closed or opened again at once, in the mode the
-- tariff has for it now.
INSERT INTO user_pools (user_id, pool_id, mode, traffic_limit)
SELECT u.id, sqlc.arg(pool_id)::BIGINT, sqlc.arg(mode)::TEXT, sqlc.narg(traffic_limit)::BIGINT
FROM users u WHERE u.tariff_id = sqlc.arg(tariff_id)::BIGINT
ON CONFLICT (user_id, pool_id) DO UPDATE SET mode = excluded.mode, traffic_limit = excluded.traffic_limit;

-- name: ListPackageTariffs :many
-- The tariffs each package is sold to; a package without rows goes to every tariff.
SELECT * FROM package_tariffs ORDER BY package_id, tariff_id;

-- name: ClearPackageTariffs :exec
DELETE FROM package_tariffs WHERE package_id = $1;

-- name: AddPackageTariff :exec
INSERT INTO package_tariffs (package_id, tariff_id) VALUES ($1, $2);

-- name: ListTariffGifts :many
SELECT * FROM tariff_gifts WHERE tariff_id = $1 ORDER BY package_id;

-- name: ListAllTariffGifts :many
SELECT * FROM tariff_gifts ORDER BY tariff_id, package_id;

-- name: ClearTariffGifts :exec
DELETE FROM tariff_gifts WHERE tariff_id = $1;

-- name: AddTariffGift :exec
INSERT INTO tariff_gifts (tariff_id, package_id) VALUES ($1, $2);

-- name: DropPackageGifts :exec
-- A package taken off the catalog is no tariff's gift any more.
DELETE FROM tariff_gifts WHERE package_id = $1;
