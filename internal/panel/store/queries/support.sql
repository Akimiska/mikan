-- name: CreateSupportGrant :one
INSERT INTO support_grants (token_hash, admin_id, level, show_people, terminal_targets, note, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSupportGrant :one
SELECT * FROM support_grants WHERE id = $1;

-- name: GetSupportGrantByHash :one
SELECT * FROM support_grants WHERE token_hash = $1;

-- name: LatestSupportGrant :one
SELECT * FROM support_grants ORDER BY id DESC LIMIT 1;

-- name: LiveSupportGrants :many
SELECT * FROM support_grants WHERE revoked_at IS NULL AND expires_at > $1 ORDER BY id;

-- name: RevokeSupportGrant :one
-- Ends a live grant; its end is written and told by whoever won this update.
UPDATE support_grants SET revoked_at = sqlc.arg(now), ended_noted_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: ExpireSupportGrants :many
-- Grants whose time ran out and whose end nobody wrote down yet.
UPDATE support_grants SET ended_noted_at = sqlc.arg(now)
WHERE revoked_at IS NULL AND ended_noted_at IS NULL AND expires_at <= sqlc.arg(now)
RETURNING *;

-- name: EnterSupportGrant :execrows
-- The one browser entry a link allows: 0 rows means it was used, revoked or has expired.
UPDATE support_grants SET entered_at = sqlc.arg(now), last_used_at = sqlc.arg(now), last_ip = sqlc.arg(ip)
WHERE id = sqlc.arg(id) AND entered_at IS NULL AND revoked_at IS NULL AND expires_at > sqlc.arg(now);

-- name: SeeSupportOperator :execrows
-- 1 row for the first sign of the operator, which the admin is told once.
UPDATE support_grants SET operator_seen_at = $2 WHERE id = $1 AND operator_seen_at IS NULL;

-- name: TouchSupportGrant :exec
UPDATE support_grants SET last_used_at = sqlc.arg(now), last_ip = sqlc.arg(ip)
WHERE id = sqlc.arg(id) AND (last_used_at IS NULL OR last_used_at < sqlc.arg(older_than));

-- name: DeleteSupportSessions :execrows
DELETE FROM sessions WHERE support_grant_id = $1;

-- name: SupportAudit :many
-- What the grant's operator did and what happened to the grant: the lines whose actor is
-- "support:<id>" (audit.ActorSupport) and the grant's own lines.
SELECT * FROM audit_log
WHERE (target_type = 'support_grant' AND target_id = sqlc.arg(grant_id)::text)
   OR details LIKE '%"actor":"support:' || sqlc.arg(grant_id)::text || '"%'
ORDER BY id DESC
LIMIT CAST(sqlc.arg(lim) AS BIGINT);

-- name: PruneSupportGrants :execrows
-- Grants that ended before the cut are forgotten (the journal keeps what they did).
DELETE FROM support_grants WHERE (revoked_at IS NOT NULL AND revoked_at < $1) OR (revoked_at IS NULL AND expires_at < $1);
