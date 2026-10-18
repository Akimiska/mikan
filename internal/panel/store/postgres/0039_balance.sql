-- +goose Up
-- The internal balance of a Telegram account: money it topped up by hand (a transfer and a
-- screenshot the admin confirms) and spends on subscriptions, renewals and extras. Amounts
-- are in minor units of the balance's currency (pay_config.balance.currency).
CREATE TABLE balances (
  tg_id      BIGINT PRIMARY KEY,
  amount     BIGINT NOT NULL DEFAULT 0 CHECK (amount >= 0),
  updated_at BIGINT NOT NULL
);
-- Every change of a balance, for the account's history and the admin's audit.
CREATE TABLE balance_ops (
  id         BIGSERIAL PRIMARY KEY,
  tg_id      BIGINT NOT NULL,
  amount     BIGINT NOT NULL,            -- + in, - out
  kind       TEXT NOT NULL CHECK (kind IN ('topup', 'spend', 'refund', 'adjust')),
  payment_id BIGINT REFERENCES payments(id) ON DELETE SET NULL,
  topup_id   BIGINT,
  admin_id   BIGINT,
  note       TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL
);
CREATE INDEX balance_ops_tg ON balance_ops (tg_id, id DESC);
-- A top-up the account asked for: the amount, the screenshot of the transfer (a Telegram
-- file id), and the admin's answer.
CREATE TABLE topups (
  id          BIGSERIAL PRIMARY KEY,
  tg_id       BIGINT NOT NULL,
  amount      BIGINT NOT NULL CHECK (amount > 0 OR status = 'draft'), -- a draft of 0 waits for its amount
  currency    TEXT NOT NULL,
  photo       TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('draft', 'pending', 'approved', 'rejected')),
  created_at  BIGINT NOT NULL,
  decided_at  BIGINT,
  admin_id    BIGINT,
  reason      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX topups_status ON topups (status, id DESC);
CREATE INDEX topups_tg ON topups (tg_id, id DESC);

-- A payment from the balance: provider "balance", in the balance's currency.
ALTER TABLE payments DROP CONSTRAINT payments_provider_check;
ALTER TABLE payments ADD CONSTRAINT payments_provider_check CHECK (provider IN ('stars', 'yookassa', 'cryptobot', 'balance') OR provider LIKE 'addon:%');
ALTER TABLE payments DROP CONSTRAINT payments_currency_check;
ALTER TABLE payments ADD CONSTRAINT payments_currency_check CHECK (currency ~ '^[A-Z]{3,4}$');

-- +goose Down
ALTER TABLE payments DROP CONSTRAINT payments_currency_check;
ALTER TABLE payments ADD CONSTRAINT payments_currency_check CHECK (currency IN ('XTR', 'RUB')) NOT VALID;
ALTER TABLE payments DROP CONSTRAINT payments_provider_check;
ALTER TABLE payments ADD CONSTRAINT payments_provider_check CHECK (provider IN ('stars', 'yookassa', 'cryptobot') OR provider LIKE 'addon:%') NOT VALID;
DROP TABLE topups;
DROP TABLE balance_ops;
DROP TABLE balances;
