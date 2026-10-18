-- name: CreateCampaign :one
INSERT INTO campaigns (code, name, created_at) VALUES ($1, $2, $3) RETURNING *;

-- name: ListCampaigns :many
SELECT * FROM campaigns ORDER BY archived, id DESC;

-- name: GetCampaignByCode :one
SELECT * FROM campaigns WHERE code = $1;

-- name: UpdateCampaign :one
UPDATE campaigns SET name = $1, archived = $2 WHERE id = $3 RETURNING *;

-- name: DeleteCampaign :execrows
DELETE FROM campaigns WHERE id = $1;

-- name: TagCampaign :execrows
-- The first campaign an account comes by, while it has no subscription: a customer who
-- taps an ad again is not the ad's.
INSERT INTO tg_chats (tg_id, created_at, updated_at, campaign_id, campaign_at)
SELECT sqlc.arg(tg_id), sqlc.arg(now), sqlc.arg(now), sqlc.arg(campaign_id), sqlc.arg(now)
WHERE NOT EXISTS (SELECT 1 FROM tg_links WHERE tg_links.tg_id = sqlc.arg(tg_id))
ON CONFLICT (tg_id) DO UPDATE SET campaign_id = excluded.campaign_id, campaign_at = excluded.campaign_at
WHERE tg_chats.campaign_id IS NULL;

-- name: CampaignArrivals :many
SELECT campaign_id, count(*) AS came FROM tg_chats
WHERE campaign_id IS NOT NULL AND campaign_at >= sqlc.arg(since)
GROUP BY campaign_id;

-- name: CampaignPayments :many
-- The applied payments of the campaigns' accounts since a moment.
SELECT ch.campaign_id, p.tg_id, p.kind, p.amount, p.currency
FROM payments p JOIN tg_chats ch ON ch.tg_id = p.tg_id
WHERE ch.campaign_id IS NOT NULL AND p.status = 'applied' AND p.applied_at >= sqlc.arg(since);
