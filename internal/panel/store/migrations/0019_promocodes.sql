-- +goose Up
CREATE TABLE promo_codes (
  id INTEGER PRIMARY KEY,
  code TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL CHECK (type IN ('days','traffic','percent','fixed')),
  value INTEGER NOT NULL CHECK (value > 0),
  currency TEXT NOT NULL DEFAULT '' CHECK (currency IN ('','XTR','RUB')),
  starts_at INTEGER,
  ends_at INTEGER,
  max_uses INTEGER CHECK (max_uses IS NULL OR max_uses > 0),
  used_count INTEGER NOT NULL DEFAULT 0 CHECK (used_count >= 0),
  per_user_limit INTEGER NOT NULL DEFAULT 1 CHECK (per_user_limit > 0),
  discount_ttl INTEGER NOT NULL DEFAULT 0 CHECK (discount_ttl >= 0),
  min_order INTEGER NOT NULL DEFAULT 0 CHECK (min_order >= 0),
  max_discount INTEGER NOT NULL DEFAULT 0 CHECK (max_discount >= 0),
  tariff_ids TEXT NOT NULL DEFAULT '[]',
  pool_id INTEGER REFERENCES traffic_pools(id) ON DELETE RESTRICT,
  first_purchase_only INTEGER NOT NULL DEFAULT 0 CHECK (first_purchase_only IN (0,1)),
  new_users_only INTEGER NOT NULL DEFAULT 0 CHECK (new_users_only IN (0,1)),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
  deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0,1)),
  created_at INTEGER NOT NULL,
  created_by INTEGER REFERENCES admins(id) ON DELETE SET NULL
);
CREATE INDEX promo_codes_active ON promo_codes(enabled, deleted, starts_at, ends_at);
CREATE INDEX promo_codes_type ON promo_codes(type);
CREATE UNIQUE INDEX promo_codes_code_active ON promo_codes(code) WHERE deleted=0;

CREATE TABLE promo_redemptions (
  id INTEGER PRIMARY KEY,
  promo_id INTEGER NOT NULL REFERENCES promo_codes(id) ON DELETE CASCADE,
  user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  tg_id INTEGER NOT NULL,
  payment_id INTEGER UNIQUE REFERENCES payments(id) ON DELETE SET NULL,
  status TEXT NOT NULL CHECK (status IN ('reserved','applied','released')),
  refund_started_at INTEGER,
  redeemed_at INTEGER NOT NULL,
  expires_at INTEGER,
  days INTEGER NOT NULL DEFAULT 0,
  bytes INTEGER NOT NULL DEFAULT 0,
  discount_amount INTEGER NOT NULL DEFAULT 0,
  original_amount INTEGER NOT NULL DEFAULT 0,
  final_amount INTEGER NOT NULL DEFAULT 0,
  currency TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT ''
);
CREATE INDEX promo_redemptions_promo ON promo_redemptions(promo_id, redeemed_at);
CREATE INDEX promo_redemptions_user ON promo_redemptions(user_id, redeemed_at);
CREATE INDEX promo_redemptions_payment ON promo_redemptions(payment_id);
-- +goose Down
DROP TABLE promo_redemptions;
DROP TABLE promo_codes;
