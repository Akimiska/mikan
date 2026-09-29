-- +goose Up
-- The Telegram bot: which account owns which subscription, per-chat state and the
-- notifications already sent.
CREATE TABLE tg_links (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, -- one owner per subscription
  tg_id      INTEGER NOT NULL,                                          -- Telegram user id (= private chat id)
  created_at INTEGER NOT NULL
);
CREATE INDEX tg_links_tg ON tg_links(tg_id);

CREATE TABLE tg_chats (
  tg_id       INTEGER PRIMARY KEY,
  username    TEXT NOT NULL DEFAULT '',
  first_name  TEXT NOT NULL DEFAULT '',
  menu_msg_id INTEGER NOT NULL DEFAULT 0, -- the bot's menu message: edited in place, replaced on new input
  current     INTEGER NOT NULL DEFAULT 0, -- the subscription shown when several are linked
  blocked     INTEGER NOT NULL DEFAULT 0, -- the user blocked the bot: nothing is sent
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE tg_notices (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind    TEXT NOT NULL,    -- expire_3d | expire_1d | expired | traffic_90 | traffic_100
  period  INTEGER NOT NULL, -- the expiry or traffic period the notice is about
  sent_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, kind, period)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE tg_notices;
DROP TABLE tg_chats;
DROP TABLE tg_links;
