-- +goose Up
-- The Telegram accounts that ever had a subscription linked: an unlink keeps the trace, so a
-- code "for new users only" and the trial are not taken again by unlinking one's own
-- subscription first. Filled from the links and the payments there are.
CREATE TABLE tg_clients (
  tg_id BIGINT PRIMARY KEY,
  since BIGINT NOT NULL
);
INSERT INTO tg_clients (tg_id, since)
SELECT tg_id, MIN(at) FROM (
  SELECT tg_id, created_at AS at FROM tg_links
  UNION ALL
  SELECT tg_id, created_at AS at FROM payments WHERE status IN ('paid', 'applied', 'refunded') AND tg_id <> 0
) AS seen
GROUP BY tg_id
ON CONFLICT (tg_id) DO NOTHING;

-- +goose Down
DROP TABLE tg_clients;
