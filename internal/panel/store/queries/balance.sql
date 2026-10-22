-- name: GetBalance :one
SELECT amount FROM balances WHERE tg_id = $1;

-- name: CreditBalance :one
INSERT INTO balances (tg_id, amount, updated_at) VALUES ($1, $2, $3)
ON CONFLICT (tg_id) DO UPDATE SET amount = balances.amount + EXCLUDED.amount, updated_at = EXCLUDED.updated_at
RETURNING amount;

-- name: DebitBalance :execrows
UPDATE balances SET amount = amount - sqlc.arg(amount), updated_at = sqlc.arg(updated_at)
WHERE tg_id = sqlc.arg(tg_id) AND amount >= sqlc.arg(amount);

-- name: AddBalanceOp :exec
INSERT INTO balance_ops (tg_id, amount, kind, payment_id, topup_id, admin_id, note, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListBalanceOps :many
SELECT * FROM balance_ops WHERE tg_id = $1 ORDER BY id DESC LIMIT $2;

-- name: BalanceOpOfPayment :one
SELECT * FROM balance_ops WHERE payment_id = $1 AND kind = $2 LIMIT 1;

-- name: CreateTopup :one
INSERT INTO topups (tg_id, amount, currency, status, created_at) VALUES ($1, $2, $3, 'draft', $4) RETURNING *;

-- name: DraftTopupOf :one
SELECT * FROM topups WHERE tg_id = $1 AND status = 'draft' ORDER BY id DESC LIMIT 1;

-- name: SetTopupAmount :execrows
UPDATE topups SET amount = $1 WHERE id = $2 AND status = 'draft';

-- name: SubmitTopup :execrows
UPDATE topups SET photo = $1, status = 'pending', created_at = $2 WHERE id = $3 AND status = 'draft' AND amount > 0;

-- name: DropDraftTopups :exec
DELETE FROM topups WHERE tg_id = $1 AND status = 'draft';

-- name: GetTopup :one
SELECT * FROM topups WHERE id = $1;

-- name: DecideTopup :execrows
UPDATE topups SET status = $1, decided_at = $2, admin_id = $3, reason = $4 WHERE id = $5 AND status = 'pending';

-- name: ListTopups :many
-- before: the page after the one that ended with that id; 0: the first.
SELECT * FROM topups WHERE status <> 'draft' AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(before)::bigint = 0 OR id < sqlc.arg(before)::bigint)
ORDER BY id DESC LIMIT sqlc.arg(lim);

-- name: CountPendingTopups :one
SELECT count(*) FROM topups WHERE status = 'pending';

-- name: PendingTopupsOf :one
SELECT count(*) FROM topups WHERE tg_id = $1 AND status = 'pending';

-- name: BalanceMoneyHeld :one
-- Money the balances hold in the currency of the config: a balance above zero, a top-up not
-- decided yet, or a balance payment that may still be refunded onto a balance. The currency
-- cannot change under it: the sums carry no currency of their own.
SELECT CAST(EXISTS (SELECT 1 FROM balances WHERE amount > 0)
    OR EXISTS (SELECT 1 FROM topups WHERE status IN ('draft', 'pending') AND amount > 0)
    OR EXISTS (SELECT 1 FROM payments WHERE provider = 'balance' AND status IN ('pending', 'paid', 'applied')) AS BOOLEAN)
  AS held;
