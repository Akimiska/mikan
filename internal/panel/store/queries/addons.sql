-- name: GetAddonGrant :one
SELECT * FROM addon_grants WHERE addon_id = $1;

-- name: ListAddonGrants :many
SELECT * FROM addon_grants ORDER BY addon_id;

-- name: UpsertAddonGrant :exec
INSERT INTO addon_grants (addon_id, version, permissions, approved_by, approved_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (addon_id) DO UPDATE SET version = EXCLUDED.version, permissions = EXCLUDED.permissions,
  approved_by = EXCLUDED.approved_by, approved_at = EXCLUDED.approved_at;

-- name: DeleteAddonGrant :exec
DELETE FROM addon_grants WHERE addon_id = $1;

-- name: GetAddonValue :one
SELECT * FROM addon_kv WHERE addon_id = $1 AND key = $2 AND (expires_at IS NULL OR expires_at > sqlc.arg(now)::BIGINT);

-- name: LockAddonValue :one
SELECT * FROM addon_kv WHERE addon_id = $1 AND key = $2 FOR UPDATE;

-- name: PutAddonValue :exec
INSERT INTO addon_kv (addon_id, key, value, version, expires_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (addon_id, key) DO UPDATE SET value = EXCLUDED.value, version = EXCLUDED.version,
  expires_at = EXCLUDED.expires_at, updated_at = EXCLUDED.updated_at;

-- name: DeleteAddonValue :execrows
DELETE FROM addon_kv WHERE addon_id = $1 AND key = $2;

-- name: AddonKVSize :one
SELECT COALESCE(sum(octet_length(value) + octet_length(key)), 0)::BIGINT AS size
FROM addon_kv WHERE addon_id = $1 AND key <> $2 AND (expires_at IS NULL OR expires_at > sqlc.arg(now)::BIGINT);

-- name: ListAddonKeys :many
SELECT key, version, expires_at FROM addon_kv
WHERE addon_id = $1 AND starts_with(key, sqlc.arg(prefix)::TEXT) AND key > sqlc.arg(after)::TEXT
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now)::BIGINT)
ORDER BY key LIMIT sqlc.arg(lim)::INT;

-- name: PruneAddonKV :execrows
DELETE FROM addon_kv WHERE expires_at IS NOT NULL AND expires_at <= sqlc.arg(now)::BIGINT;
