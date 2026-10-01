-- +goose Up
-- Selling tariffs in the bot and the Mini App. A tariff has a price in Telegram Stars and
-- one in rubles (YooKassa, and CryptoBot, which turns rubles into the coin the payer picks).
ALTER TABLE tariffs ADD COLUMN price_stars INTEGER;                -- NULL: not sold for Stars
ALTER TABLE tariffs ADD COLUMN price_rub INTEGER;                  -- kopecks; NULL: not sold for rubles
ALTER TABLE tariffs ADD COLUMN on_sale INTEGER NOT NULL DEFAULT 0; -- offered to buyers

-- One row per invoice. The payload is ours and unguessable; the provider's id is unique,
-- so a payment that comes twice (a repeated webhook, a reconcile pass) is applied once.
CREATE TABLE payments (
  id          INTEGER PRIMARY KEY,
  provider    TEXT NOT NULL CHECK (provider IN ('stars', 'yookassa', 'cryptobot')),
  payload     TEXT NOT NULL UNIQUE,
  external_id TEXT,                                               -- charge, payment or invoice id
  tg_id       INTEGER NOT NULL,                                   -- the buyer
  kind        TEXT NOT NULL CHECK (kind IN ('new', 'renew')),
  user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,    -- renewed, or created on apply
  tariff_id   INTEGER NOT NULL REFERENCES tariffs(id),
  tariff_name TEXT NOT NULL,                                      -- as it was when bought
  amount      INTEGER NOT NULL,                                   -- Stars, or kopecks
  currency    TEXT NOT NULL CHECK (currency IN ('XTR', 'RUB')),
  status      TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'applied', 'expired', 'failed', 'refunded')),
  error       TEXT NOT NULL DEFAULT '',                           -- why a paid one is not applied yet
  pay_url     TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  paid_at     INTEGER,
  applied_at  INTEGER,
  refunded_at INTEGER
);
CREATE UNIQUE INDEX payments_external ON payments(provider, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX payments_status ON payments(status, created_at);
CREATE INDEX payments_user ON payments(user_id);

-- +goose Down
DROP TABLE payments;
ALTER TABLE tariffs DROP COLUMN on_sale;
ALTER TABLE tariffs DROP COLUMN price_rub;
ALTER TABLE tariffs DROP COLUMN price_stars;
