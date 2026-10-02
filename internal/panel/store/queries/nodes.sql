-- name: ListNodes :many
SELECT * FROM nodes ORDER BY id;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: CreateNode :one
INSERT INTO nodes (name, address, public_host, domain, cert_sha256, enabled, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, 1, $6, $7)
RETURNING *;

-- name: UpdateNode :one
UPDATE nodes SET name = $1, address = $2, public_host = $3, domain = $4, enabled = $5, updated_at = $6 WHERE id = $7 RETURNING *;

-- name: SetNodeCert :exec
UPDATE nodes SET cert_sha256 = $1, updated_at = $2 WHERE id = $3;

-- name: DeleteNode :exec
DELETE FROM nodes WHERE id = $1 AND id != 1;
