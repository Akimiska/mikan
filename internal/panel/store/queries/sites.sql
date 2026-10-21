-- name: AddUserSite :exec
INSERT INTO user_sites (day, user_id, site, up, down) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, day, site) DO UPDATE SET up = user_sites.up + EXCLUDED.up, down = user_sites.down + EXCLUDED.down;

-- name: UserSites :many
SELECT site, sum(up)::bigint AS up, sum(down)::bigint AS down FROM user_sites
WHERE user_id = sqlc.arg(user_id) AND day >= sqlc.arg(since)
GROUP BY site ORDER BY sum(up) + sum(down) DESC LIMIT sqlc.arg(lim);

-- name: PruneUserSites :execrows
DELETE FROM user_sites WHERE day < $1;
