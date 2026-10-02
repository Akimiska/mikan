-- name: GetPromoCode :one
SELECT * FROM promo_codes WHERE id = ?;

-- name: GetPromoCodeByCode :one
SELECT * FROM promo_codes WHERE code = ? AND deleted=0;

-- name: ListPromoCodes :many
SELECT * FROM promo_codes WHERE deleted = 0 ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: CountPromoCodes :one
SELECT COUNT(*) FROM promo_codes WHERE deleted = 0;

-- name: CountActivePromoCodes :one
SELECT COUNT(*) FROM promo_codes WHERE deleted=0 AND enabled=1
  AND (starts_at IS NULL OR starts_at<=?) AND (ends_at IS NULL OR ends_at>?)
  AND (max_uses IS NULL OR used_count<max_uses);

-- name: CreatePromoCode :one
INSERT INTO promo_codes
(code,name,description,type,value,currency,starts_at,ends_at,max_uses,per_user_limit,discount_ttl,min_order,max_discount,tariff_ids,pool_id,first_purchase_only,new_users_only,enabled,deleted,created_at,created_by)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?,?) RETURNING *;

-- name: UpdatePromoCode :one
UPDATE promo_codes SET
code=?,name=?,description=?,type=?,value=?,currency=?,starts_at=?,ends_at=?,max_uses=?,per_user_limit=?,discount_ttl=?,min_order=?,max_discount=?,tariff_ids=?,pool_id=?,first_purchase_only=?,new_users_only=?,enabled=? WHERE id=? AND deleted=0 RETURNING *;

-- name: DeletePromoCode :execrows
UPDATE promo_codes SET deleted=1, enabled=0, pool_id=NULL WHERE id=? AND deleted=0;

-- name: CountEnabledPromoCodesForPool :one
SELECT COUNT(*) FROM promo_codes WHERE pool_id=? AND enabled=1 AND deleted=0;

-- name: ClearDisabledPromoPools :exec
UPDATE promo_codes SET pool_id=NULL WHERE pool_id=? AND (enabled=0 OR deleted=1);

-- name: ClaimLatePromoRefund :execrows
UPDATE promo_redemptions SET refund_started_at=? WHERE id=?
  AND (status='released' OR (status='reserved' AND expires_at IS NOT NULL AND expires_at<=?))
  AND (refund_started_at IS NULL OR refund_started_at<=?);

-- name: SetPromoEnabled :execrows
UPDATE promo_codes SET enabled=? WHERE id=? AND deleted=0;

-- name: IncrementPromoUse :execrows
UPDATE promo_codes SET used_count=used_count+1 WHERE id=? AND (max_uses IS NULL OR used_count < max_uses) AND deleted=0 AND enabled=1;

-- name: DecrementPromoUse :execrows
UPDATE promo_codes SET used_count=CASE WHEN used_count>0 THEN used_count-1 ELSE 0 END WHERE id=?;

-- name: CountUserPaidPayments :one
SELECT COUNT(*) FROM payments WHERE tg_id=? AND status IN ('paid','applied');

-- name: CountPromoUser :one
SELECT COUNT(*) FROM promo_redemptions WHERE promo_id=? AND (tg_id=? OR user_id=?) AND status IN ('reserved','applied');

-- name: CreatePromoRedemption :one
INSERT INTO promo_redemptions
(promo_id,user_id,tg_id,payment_id,status,redeemed_at,expires_at,days,bytes,discount_amount,original_amount,final_amount,currency,note)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING *;

-- name: GetPromoRedemption :one
SELECT * FROM promo_redemptions WHERE id=?;

-- name: GetPromoRedemptionByPayment :one
SELECT * FROM promo_redemptions WHERE payment_id=?;

-- name: ListPromoRedemptions :many
SELECT * FROM promo_redemptions ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: CountPromoRedemptions :one
SELECT COUNT(*) FROM promo_redemptions;

-- name: MarkPromoApplied :execrows
UPDATE promo_redemptions SET status='applied', user_id=COALESCE(?,user_id) WHERE id=? AND status='reserved';

-- name: ReleasePromoRedemption :execrows
UPDATE promo_redemptions SET status='released' WHERE id=? AND status='reserved';

-- name: ReleasePromoRedemptionForClosedPayment :execrows
UPDATE promo_redemptions SET status='released'
WHERE promo_redemptions.id=? AND promo_redemptions.status='reserved' AND EXISTS (
  SELECT 1 FROM payments p WHERE p.id=promo_redemptions.payment_id AND p.status IN ('failed','expired','refunded')
);

-- name: ReleasePromoByPayment :execrows
UPDATE promo_redemptions SET status='released' WHERE payment_id=? AND status='reserved';

-- name: PromoStats :one
SELECT COUNT(CASE WHEN status='applied' THEN 1 END), COALESCE(SUM(CASE WHEN status='applied' THEN days ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='applied' THEN bytes ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='applied' THEN discount_amount ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='applied' AND payment_id IS NOT NULL THEN 1 ELSE 0 END),0) FROM promo_redemptions;

-- name: ListExpiredPromoPayments :many
SELECT DISTINCT p.id FROM payments p JOIN promo_redemptions r ON r.payment_id=p.id WHERE p.status='expired' AND p.created_at < ? AND r.status='reserved';

-- name: ListPromoRedemptionsByTg :many
SELECT * FROM promo_redemptions WHERE tg_id=? AND status='applied' ORDER BY id DESC LIMIT ?;
