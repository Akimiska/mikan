-- name: AddLegacySubToken :exec
INSERT INTO legacy_sub_tokens (token, user_id, source) VALUES ($1, $2, $3)
ON CONFLICT (token) DO NOTHING;

-- name: LegacySubTokenUser :one
SELECT u.* FROM legacy_sub_tokens t JOIN users u ON u.id = t.user_id WHERE t.token = $1;

-- name: CountLegacySubTokens :one
SELECT count(*) FROM legacy_sub_tokens;

-- name: SetImportedUsage :exec
UPDATE users SET used_down = $2, total_down = $3, updated_at = $4 WHERE id = $1;

-- name: UserNameTaken :one
SELECT EXISTS (SELECT 1 FROM users WHERE name = $1);
