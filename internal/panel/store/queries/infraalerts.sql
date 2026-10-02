-- name: GetInfrastructureAlertState :one
SELECT value FROM infrastructure_alert_state WHERE key = ?;

-- name: SetInfrastructureAlertState :exec
INSERT INTO infrastructure_alert_state (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: MaxInboundEventID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM inbound_events;
