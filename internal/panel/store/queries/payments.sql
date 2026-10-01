-- name: CreatePayment :one
INSERT INTO payments (provider, payload, tg_id, kind, user_id, tariff_id, tariff_name, amount, currency, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)
RETURNING *;

-- name: GetPayment :one
SELECT * FROM payments WHERE id = ?;

-- name: GetPaymentByPayload :one
SELECT * FROM payments WHERE payload = ?;

-- name: GetPaymentByExternal :one
SELECT * FROM payments WHERE provider = ? AND external_id = ?;

-- name: SetPaymentInvoice :exec
UPDATE payments SET external_id = ?, pay_url = ? WHERE id = ?;

-- name: MarkPaymentPaid :execrows
UPDATE payments SET status = 'paid', external_id = ?, paid_at = ?
WHERE id = ? AND status IN ('pending', 'expired');

-- name: MarkPaymentApplied :execrows
UPDATE payments SET status = 'applied', user_id = ?, applied_at = ?, error = ''
WHERE id = ? AND status = 'paid';

-- name: SetPaymentError :exec
UPDATE payments SET error = ? WHERE id = ?;

-- name: SetPaymentStatus :execrows
UPDATE payments SET status = sqlc.arg(new_status) WHERE id = sqlc.arg(id) AND status = sqlc.arg(old_status);

-- name: MarkPaymentRefunded :execrows
UPDATE payments SET status = 'refunded', refunded_at = ? WHERE id = ? AND status = 'applied';

-- name: FindOpenPayment :one
SELECT * FROM payments
WHERE tg_id = ? AND tariff_id = ? AND provider = ? AND kind = ? AND IFNULL(user_id, 0) = sqlc.arg(user_id)
  AND status = 'pending' AND pay_url <> '' AND created_at > sqlc.arg(since)
ORDER BY id DESC LIMIT 1;

-- name: ListOpenPayments :many
SELECT * FROM payments WHERE status IN ('pending', 'paid') AND created_at > ? ORDER BY id;

-- name: ExpirePayments :execrows
UPDATE payments SET status = 'expired' WHERE status = 'pending' AND created_at < ?;

-- name: ListPayments :many
SELECT * FROM payments
WHERE id < sqlc.arg(before_id)
  AND (sqlc.arg(status) = '' OR status = sqlc.arg(status))
  AND (sqlc.arg(provider) = '' OR provider = sqlc.arg(provider))
  AND (sqlc.arg(user_id) = 0 OR user_id = sqlc.arg(user_id))
ORDER BY id DESC LIMIT sqlc.arg(lim);

-- name: PaymentTotals :many
SELECT currency, count(*) AS n, CAST(IFNULL(sum(amount), 0) AS INTEGER) AS total
FROM payments WHERE status = 'applied' AND applied_at >= ? GROUP BY currency;

-- name: ListTariffsOnSale :many
SELECT * FROM tariffs WHERE archived = 0 AND on_sale = 1 ORDER BY sort, id;
