-- name: ListTariffs :many
SELECT * FROM tariffs WHERE archived = 0 ORDER BY sort, id;

-- name: GetTariff :one
SELECT * FROM tariffs WHERE id = ?;

-- name: CountTariffs :one
SELECT count(*) FROM tariffs;

-- name: CreateTariff :one
INSERT INTO tariffs (name, traffic_limit, duration_days, device_limit, reset_strategy, price_label, sort, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateTariff :one
UPDATE tariffs
SET name = ?, traffic_limit = ?, duration_days = ?, device_limit = ?, reset_strategy = ?, price_label = ?, sort = ?
WHERE id = ?
RETURNING *;

-- name: ArchiveTariff :exec
UPDATE tariffs SET archived = 1 WHERE id = ?;

-- name: CountSlotsByState :many
SELECT state, count(*) AS n FROM slots GROUP BY state;

-- name: InsertSlot :exec
INSERT INTO slots (name, uuid, secret, state, created_at) VALUES (?, ?, ?, 'free', ?);

-- name: MaxSlotID :one
SELECT CAST(coalesce(max(id), 0) AS INTEGER) FROM slots;

-- name: TakeFreeSlot :one
UPDATE slots SET state = 'assigned'
WHERE id = (SELECT id FROM slots WHERE state = 'free' ORDER BY id LIMIT 1)
RETURNING *;

-- name: BurnSlot :exec
UPDATE slots SET state = 'burned', burned_at = ? WHERE id = ?;

-- name: DeleteBurnedSlots :exec
DELETE FROM slots WHERE state = 'burned' AND id NOT IN (SELECT slot_id FROM users WHERE slot_id IS NOT NULL);

-- name: ListSlots :many
SELECT * FROM slots ORDER BY id;

-- name: GetSlot :one
SELECT * FROM slots WHERE id = ?;

-- name: ListSlotUsers :many
SELECT s.name AS slot_name, u.id AS user_id FROM slots s JOIN users u ON u.slot_id = s.id;

-- name: CreateUser :one
INSERT INTO users (name, contact, note, tags, status, tariff_id, traffic_limit, device_limit, reset_strategy,
                   period_days, period_start, expires_at, inbounds, sub_token, slot_id, created_at, updated_at)
VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserBySubToken :one
SELECT * FROM users WHERE sub_token = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY id DESC;

-- name: UpdateUser :one
UPDATE users
SET name = ?, contact = ?, note = ?, tags = ?, status = ?, tariff_id = ?, traffic_limit = ?, device_limit = ?,
    reset_strategy = ?, period_days = ?, period_start = ?, expires_at = ?, inbounds = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: SetUserCredentials :exec
UPDATE users SET slot_id = ?, sub_token = ?, updated_at = ? WHERE id = ?;

-- name: ResetUserTraffic :exec
UPDATE users SET used_up = 0, used_down = 0, period_start = ?, updated_at = ? WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: AddUserTraffic :exec
UPDATE users
SET used_up = used_up + sqlc.arg(up), used_down = used_down + sqlc.arg(down),
    total_up = total_up + sqlc.arg(up), total_down = total_down + sqlc.arg(down)
WHERE id = sqlc.arg(id);

-- name: SetUserOnline :exec
UPDATE users SET online_at = ? WHERE id = ?;

-- name: AddTrafficHourly :exec
INSERT INTO traffic_hourly (user_id, hour, up, down) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, hour) DO UPDATE SET up = up + excluded.up, down = down + excluded.down;

-- name: AddTrafficDaily :exec
INSERT INTO traffic_daily (user_id, day, up, down) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down;

-- name: UserTrafficHourly :many
SELECT hour, up, down FROM traffic_hourly WHERE user_id = ? AND hour >= ? ORDER BY hour;

-- name: UserTrafficDaily :many
SELECT day, up, down FROM traffic_daily WHERE user_id = ? AND day >= ? ORDER BY day;

-- name: TotalTrafficHourly :many
SELECT hour, CAST(sum(up) AS INTEGER) AS up, CAST(sum(down) AS INTEGER) AS down
FROM traffic_hourly WHERE hour >= ? GROUP BY hour ORDER BY hour;

-- name: TotalTrafficDaily :many
SELECT day, CAST(sum(up) AS INTEGER) AS up, CAST(sum(down) AS INTEGER) AS down
FROM traffic_daily WHERE day >= ? GROUP BY day ORDER BY day;

-- name: TopUsersByTraffic :many
SELECT u.id, u.name, CAST(sum(d.up + d.down) AS INTEGER) AS bytes
FROM traffic_daily d JOIN users u ON u.id = d.user_id
WHERE d.day >= ?
GROUP BY u.id ORDER BY bytes DESC LIMIT ?;

-- name: PruneTrafficHourly :exec
DELETE FROM traffic_hourly WHERE hour < ?;

-- name: UpsertDevice :exec
INSERT INTO devices (user_id, ip, first_seen, last_seen) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, ip) DO UPDATE SET last_seen = excluded.last_seen;

-- name: SetDeviceClient :exec
UPDATE devices SET client = ? WHERE user_id = ? AND ip = ?;

-- name: ListUserDevices :many
SELECT * FROM devices WHERE user_id = ? ORDER BY last_seen DESC;

-- name: PruneDevices :exec
DELETE FROM devices WHERE last_seen < ?;

-- name: ListInbounds :many
SELECT * FROM inbounds ORDER BY id;

-- name: GetInbound :one
SELECT * FROM inbounds WHERE id = ?;

-- name: CreateInbound :one
INSERT INTO inbounds (name, preset, port, enabled, settings, created_at, updated_at)
VALUES (?, ?, ?, 1, ?, ?, ?)
RETURNING *;

-- name: UpdateInbound :one
UPDATE inbounds SET port = ?, enabled = ?, settings = ?, updated_at = ? WHERE id = ? RETURNING *;

-- name: DeleteInbound :exec
DELETE FROM inbounds WHERE id = ?;

-- name: GetNodeState :one
SELECT value FROM node_state WHERE key = ?;

-- name: SetNodeState :exec
INSERT INTO node_state (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value;
