package aoiapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/server"
	"mikan/internal/panel/store/db"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) clientIP(r *http.Request) string {
	return server.ClientIP(r.Header, r.RemoteAddr, h.d.TrustProxy)
}

// serverTime is how the legacy bot wrote times: UTC without a zone.
func serverTime(unix sql.NullInt64) any {
	if !unix.Valid {
		return nil
	}
	return time.Unix(unix.Int64, 0).UTC().Format("2006-01-02 15:04:05")
}

func (h *Handler) subURL(ctx context.Context, u db.User) any {
	if h.d.SubBase == nil || u.SubToken == "" {
		return nil
	}
	if base := h.d.SubBase(ctx); base != "" {
		return base + "/" + u.SubToken
	}
	return nil
}

// active: the subscription can connect now and has a term (a subscription without an
// end is not one the app sold).
func (h *Handler) active(u db.User) bool {
	return u.Status != "disabled" && u.ExpiresAt.Valid && h.d.Now().Unix() < u.ExpiresAt.Int64
}

// --- plans ------------------------------------------------------------------------

type plan struct {
	Months       int64  `json:"months"`
	Days         int64  `json:"days"`
	AmountRub    int64  `json:"amount_rub"`
	DiscountPct  int64  `json:"discount_pct"`
	FullPriceRub int64  `json:"full_price_rub"`
	Label        string `json:"label"`
}

// planTerms are the app tariff's terms with a ruble price now, and the provider to pay
// them with ("" when no adapter takes rubles).
func (h *Handler) planTerms(ctx context.Context) ([]billing.OfferTerm, string, error) {
	offers, av, err := h.d.Billing.Offers(ctx)
	if err != nil {
		return nil, "", err
	}
	provider := ""
	if len(av.Addons) > 0 {
		provider = billing.AddonPrefix + av.Addons[0]
	}
	for _, o := range offers {
		if o.Tariff.ID != h.tariffID {
			continue
		}
		var terms []billing.OfferTerm
		for _, t := range o.Terms {
			if t.Rub > 0 && t.Days%30 == 0 {
				terms = append(terms, t)
			}
		}
		return terms, provider, nil
	}
	return nil, provider, nil
}

func monthsLabel(n int64) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return strconv.FormatInt(n, 10) + " месяц"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return strconv.FormatInt(n, 10) + " месяца"
	}
	return strconv.FormatInt(n, 10) + " месяцев"
}

func (h *Handler) plans(w http.ResponseWriter, r *http.Request) {
	terms, _, err := h.planTerms(r.Context())
	if err != nil {
		h.d.Log.Warn("aoiapp: plans", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "internal"})
		return
	}
	// The monthly price is the base the longer terms' discount is counted from.
	var monthly int64
	for _, t := range terms {
		if t.Days == 30 {
			monthly = t.Rub / 100
		}
	}
	out := []plan{}
	for _, t := range terms {
		m := t.Days / 30
		amount := t.Rub / 100
		full, pct := amount, int64(0)
		if monthly > 0 {
			full = monthly * m
			if full > amount {
				pct = int64(math.Round(float64(full-amount) * 100 / float64(full)))
			} else {
				full = amount
			}
		}
		out = append(out, plan{Months: m, Days: t.Days, AmountRub: amount, DiscountPct: pct, FullPriceRub: full, Label: monthsLabel(m)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plans": out})
}

// --- pay --------------------------------------------------------------------------

// resolveToken is the subscription a token (the panel's, or a legacy one carried over)
// leads to.
func (h *Handler) resolveToken(ctx context.Context, token string) (db.User, error) {
	u, err := h.d.Store.Q.GetUserBySubToken(ctx, token)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return u, err
	}
	var id int64
	if err := h.d.Store.DB.QueryRowContext(ctx, "SELECT user_id FROM legacy_sub_tokens WHERE token = $1", token).Scan(&id); err != nil {
		return db.User{}, err
	}
	return h.d.Store.Q.GetUser(ctx, id)
}

func (h *Handler) pay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	appID := strings.TrimSpace(q.Get("app_id"))
	if !validAppID(appID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_app_id"})
		return
	}
	if !h.payLimit.allow(h.clientIP(r), h.d.Now()) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "rate_limited"})
		return
	}
	months, err := strconv.ParseInt(strings.TrimSpace(q.Get("months")), 10, 64)
	if err != nil || months <= 0 {
		months = 1
	}
	target := strings.TrimSpace(q.Get("target"))
	orderID := strings.TrimSpace(q.Get("order"))
	if orderID != "" && !orderIDRe.MatchString(orderID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_order"})
		return
	}
	appTg, _, err := h.installTg(ctx, appID, true)
	if err != nil {
		h.d.Log.Error("aoiapp: install", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal"})
		return
	}
	req := billing.InvoiceRequest{TgID: appTg, TariffID: h.tariffID}
	var prior db.User
	switch target {
	case "renew":
		token := normalizeToken(q.Get("sub_token"))
		if token == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "subscription_not_found"})
			return
		}
		if prior, err = h.resolveToken(ctx, token); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "subscription_not_found"})
			return
		}
		req.UserID = prior.ID
		// The token is the subscription's bearer secret: whoever holds it may pay for it.
		// The payment is the owner's (billing renews only its own); a subscription with
		// no owner yet becomes this install's.
		link, err := h.d.Store.Q.GetTgLink(ctx, prior.ID)
		switch {
		case err == nil:
			req.TgID = link.TgID
		case errors.Is(err, sql.ErrNoRows):
			if err := h.d.Store.Q.LinkTg(ctx, db.LinkTgParams{UserID: prior.ID, TgID: appTg, CreatedAt: h.d.Now().Unix()}); err != nil {
				h.d.Log.Error("aoiapp: link", "err", err)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal"})
				return
			}
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal"})
			return
		}
	case "new", "":
		// "" — clients before 0.3: their own subscription, renewed when they have one.
		if target == "" {
			if u, ok := h.mainSubscription(ctx, appTg); ok {
				req.UserID, prior = u.ID, u
			}
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_target"})
		return
	}
	terms, provider, err := h.planTerms(ctx)
	if err != nil || provider == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "provider_off"})
		return
	}
	days := months * 30
	if _, ok := findTerm(terms, days); !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Неверный период подписки."})
		return
	}
	req.TermDays, req.Provider = &days, provider
	ret := h.returnURL + "?app_id=" + url.QueryEscape(appID) + "&r=ok"
	p, err := h.d.Billing.Invoice(billing.WithReturnURL(ctx, ret), req)
	if err != nil {
		h.d.Log.Warn("aoiapp: invoice", "target", target, "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if p.PayUrl == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "no_redirect"})
		return
	}
	if orderID != "" {
		kind := "new"
		var userID, prevExp sql.NullInt64
		if req.UserID != 0 {
			kind = "renew"
			userID = sql.NullInt64{Int64: req.UserID, Valid: true}
			prevExp = prior.ExpiresAt
		}
		if _, err := h.d.Store.DB.ExecContext(ctx, `INSERT INTO aoi_app_orders
			(order_id, app_id, payment_id, kind, user_id, prev_expires_at, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (order_id) DO UPDATE SET payment_id = excluded.payment_id, kind = excluded.kind,
			user_id = excluded.user_id, prev_expires_at = excluded.prev_expires_at`,
			orderID, appID, p.ID, kind, userID, prevExp, h.d.Now().Unix()); err != nil {
			h.d.Log.Warn("aoiapp: order", "order", orderID, "err", err)
		}
	}
	h.d.Log.Info("aoiapp: pay", "target", target, "payment", p.ID, "tg", req.TgID)
	http.Redirect(w, r, p.PayUrl, http.StatusFound)
}

func findTerm(terms []billing.OfferTerm, days int64) (billing.OfferTerm, bool) {
	for _, t := range terms {
		if t.Days == days {
			return t, true
		}
	}
	return billing.OfferTerm{}, false
}

// --- subscription(s) ----------------------------------------------------------------

// mainSubscription is the install's subscription the old clients show: the active one
// ending last, else the latest.
func (h *Handler) mainSubscription(ctx context.Context, tg int64) (db.User, bool) {
	list, err := h.d.Store.Q.ListTgLinksOf(ctx, tg)
	if err != nil || len(list) == 0 {
		return db.User{}, false
	}
	best := list[len(list)-1]
	for _, u := range list {
		if h.active(u) && (!h.active(best) || u.ExpiresAt.Int64 > best.ExpiresAt.Int64) {
			best = u
		}
	}
	return best, true
}

func (h *Handler) subscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if !validAppID(appID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_app_id"})
		return
	}
	none := map[string]any{"ok": true, "active": false, "sub_url": nil, "expires_at": nil}
	tg, ok, err := h.installTg(ctx, appID, false)
	if err != nil || !ok {
		writeJSON(w, http.StatusOK, none)
		return
	}
	u, ok := h.mainSubscription(ctx, tg)
	if !ok {
		writeJSON(w, http.StatusOK, none)
		return
	}
	active := h.active(u)
	var sub any
	if active {
		sub = h.subURL(ctx, u)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "active": active, "sub_url": sub, "expires_at": serverTime(u.ExpiresAt)})
}

func (h *Handler) subscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	appID := strings.TrimSpace(q.Get("app_id"))
	if !validAppID(appID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_app_id"})
		return
	}
	var tokens []string
	for _, part := range strings.Split(strings.Join(q["tokens"], ","), ",") {
		if t := normalizeToken(part); t != "" && !contains(tokens, t) {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) > maxTokens {
		tokens = tokens[:maxTokens]
	}
	var mine []db.User
	if tg, ok, err := h.installTg(ctx, appID, false); err == nil && ok {
		mine, _ = h.d.Store.Q.ListTgLinksOf(ctx, tg)
	}
	isMine := func(id int64) bool {
		for _, u := range mine {
			if u.ID == id {
				return true
			}
		}
		return false
	}
	item := func(token string, u db.User) map[string]any {
		return map[string]any{"token": token, "found": true, "active": h.active(u),
			"expires_at": serverTime(u.ExpiresAt), "sub_url": h.subURL(ctx, u), "mine": isMine(u.ID)}
	}
	items := []map[string]any{}
	seen := map[int64]bool{}
	for _, t := range tokens {
		u, err := h.resolveToken(ctx, t)
		if err != nil {
			items = append(items, map[string]any{"token": t, "found": false, "active": false,
				"expires_at": nil, "sub_url": nil, "mine": false})
			continue
		}
		if seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		items = append(items, item(t, u))
	}
	for _, u := range mine {
		if !seen[u.ID] {
			seen[u.ID] = true
			items = append(items, item(u.SubToken, u))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- order ------------------------------------------------------------------------

// order is the state of a purchase for the app's polling: pending (not paid yet) →
// paid (paid, being applied) → ready (the subscription is made or extended);
// canceled — the payment failed, expired or was refunded.
func (h *Handler) order(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	appID, orderID := strings.TrimSpace(q.Get("app_id")), strings.TrimSpace(q.Get("order"))
	if !validAppID(appID) || !orderIDRe.MatchString(orderID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_params"})
		return
	}
	var (
		paymentID int64
		kind      string
		prevExp   sql.NullInt64
	)
	err := h.d.Store.DB.QueryRowContext(ctx,
		"SELECT payment_id, kind, prev_expires_at FROM aoi_app_orders WHERE order_id = $1 AND app_id = $2",
		orderID, appID).Scan(&paymentID, &kind, &prevExp)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "order_not_found"})
		return
	}
	p, err := h.d.Store.Q.GetPayment(ctx, paymentID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "order_not_found"})
		return
	}
	state := "pending"
	var u db.User
	switch p.Status {
	case billing.PaymentFailed, billing.PaymentExpired, billing.PaymentRefunded:
		state = "canceled"
	case billing.PaymentPaid:
		state = "paid"
	case billing.PaymentApplied:
		state = "paid"
		if p.UserID.Valid {
			if u, err = h.d.Store.Q.GetUser(ctx, p.UserID.Int64); err == nil && h.active(u) &&
				(!prevExp.Valid || u.ExpiresAt.Int64 > prevExp.Int64) {
				state = "ready"
			}
		}
	}
	var sub any
	if state == "ready" {
		sub = h.subURL(ctx, u)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": state, "kind": kind,
		"active": u.ID != 0 && h.active(u), "expires_at": serverTime(u.ExpiresAt), "sub_url": sub})
}

// --- core-report ------------------------------------------------------------------

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "")
}

func (h *Handler) coreReport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AppID       string `json:"app_id"`
		Reason      string `json:"reason"`
		CoreVersion string `json:"core_version"`
		AppVersion  string `json:"app_version"`
		LogTail     string `json:"log_tail"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	_ = json.Unmarshal(body, &in)
	appID, reason := clip(in.AppID, 64), clip(in.Reason, 200)
	if appID == "" && reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "empty"})
		return
	}
	tail := in.LogTail
	if len(tail) > 32768 {
		tail = tail[len(tail)-32768:]
	}
	if _, err := h.d.Store.DB.ExecContext(r.Context(), `INSERT INTO aoi_core_reports
		(created_at, app_id, reason, core_version, app_version, client_ip, log_tail) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		h.d.Now().Unix(), appID, reason, clip(in.CoreVersion, 64), clip(in.AppVersion, 32), h.clientIP(r),
		strings.ToValidUTF8(tail, "")); err != nil {
		h.d.Log.Warn("aoiapp: core report", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "store_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- test-sub ---------------------------------------------------------------------

func trialKey(kind, v string) string {
	sum := sha256.Sum256([]byte(v))
	return kind + ":" + hex.EncodeToString(sum[:16])
}

// testSub gives the landing's free trial (vpn.aoimusic.ru/try/) through the panel's own
// trial: once per address and per browser fingerprint, each a synthetic account.
func (h *Handler) testSub(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := h.clientIP(r)
	if !h.trialLimit.allow(ip, h.d.Now()) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "rate_limited"})
		return
	}
	if _, ok := h.d.Billing.TrialTariff(ctx); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "disabled"})
		return
	}
	var in struct {
		Fingerprint string `json:"fingerprint"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	_ = json.Unmarshal(body, &in)
	keys := []string{trialKey("ip", ip)}
	if fp := clip(in.Fingerprint, 256); fp != "" {
		keys = append(keys, trialKey("fp", fp))
	}
	var used bool
	if err := h.d.Store.DB.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM aoi_trial_keys WHERE key = ANY($1))", keys).Scan(&used); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "provision_failed"})
		return
	}
	if used {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "already_used"})
		return
	}
	tg, err := h.newSyntheticTg(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "provision_failed"})
		return
	}
	now := h.d.Now().Unix()
	for _, k := range keys {
		res, err := h.d.Store.DB.ExecContext(ctx,
			"INSERT INTO aoi_trial_keys (key, tg_id, created_at) VALUES ($1, $2, $3) ON CONFLICT (key) DO NOTHING", k, tg, now)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "provision_failed"})
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "already_used"})
			return
		}
	}
	u, err := h.d.Billing.Trial(ctx, tg)
	switch {
	case errors.Is(err, billing.ErrTrialUsed):
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "already_used"})
		return
	case errors.Is(err, domain.ErrNoSlots):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "capacity"})
		return
	case err != nil:
		h.d.Log.Warn("aoiapp: trial", "err", err)
		// Not given: the address and fingerprint may try again.
		_, _ = h.d.Store.DB.ExecContext(ctx, "DELETE FROM aoi_trial_keys WHERE tg_id = $1", tg)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "provision_failed"})
		return
	}
	var in2 int64
	if u.ExpiresAt.Valid {
		in2 = max(u.ExpiresAt.Int64-now, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sub_url": h.subURL(ctx, u),
		"expires_at": serverTime(u.ExpiresAt), "expires_in": in2})
}
