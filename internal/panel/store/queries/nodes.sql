-- name: ListNodes :many
SELECT * FROM nodes ORDER BY id;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = ?;

-- name: CreateNode :one
INSERT INTO nodes (name, address, public_host, domain, cert_sha256, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 1, ?, ?)
RETURNING *;

-- name: UpdateNode :one
UPDATE nodes SET name = ?, address = ?, public_host = ?, domain = ?, enabled = ?, updated_at = ? WHERE id = ? RETURNING *;

-- name: SetNodeCert :exec
UPDATE nodes SET cert_sha256 = ?, updated_at = ? WHERE id = ?;

-- name: DeleteNode :exec
DELETE FROM nodes WHERE id = ? AND id != 1;
