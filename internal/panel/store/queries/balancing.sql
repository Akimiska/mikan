-- name: SetNodeBalance :exec
UPDATE nodes SET capacity = $1, balance_group = $2, updated_at = $3 WHERE id = $4;

-- name: ListUserAssignments :many
SELECT * FROM node_assignments WHERE user_id = $1;

-- name: AssignNode :exec
INSERT INTO node_assignments (user_id, balance_group, node_id, assigned_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, balance_group) DO UPDATE SET node_id = excluded.node_id, assigned_at = excluded.assigned_at;

-- name: NodeLoads :many
-- The clients kept on each node who can use it now: on, and their term not over.
SELECT a.node_id, count(*) AS clients
FROM node_assignments a JOIN users u ON u.id = a.user_id
WHERE u.status = 'active' AND (u.expires_at IS NULL OR u.expires_at > sqlc.arg(now))
GROUP BY a.node_id;

-- name: ClearGroupAssignments :execrows
DELETE FROM node_assignments WHERE balance_group = $1;

-- name: ClearStaleAssignments :execrows
-- Assignments to a node that left its group: the client is placed again.
DELETE FROM node_assignments a USING nodes n WHERE n.id = a.node_id AND n.balance_group <> a.balance_group;
