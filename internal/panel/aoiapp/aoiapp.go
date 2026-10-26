// Package aoiapp is the AoiVPN fork's API for the AoiVPN app: plans, payment, the
// subscriptions an install owns, orders and the landing trial. It replaces the legacy
// Python bot's /api/app/* (vpn-bot/scripts/redirect_server.py) with the same request
// and response shapes, so the released app does not change.
//
// It is served on the subscription path as <sub path>/app/<name>; nginx maps the old
// public URLs (https://sub.aoimusic.ru/api/app/<name>, /api/test-sub) there.
//
// The app has no Telegram account, and billing keys a buyer by one: an install
// (app_id) gets a synthetic negative tg_id, as the legacy bot did. The fork's tables
// (aoi_*) are created here, not by the upstream migrations, so a merge of upstream
// never meets them.
package aoiapp

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/store"
)

// Deps are what the API works with.
type Deps struct {
	Store      *store.Store
	Billing    *billing.Service
	SubBase    func(ctx context.Context) string
	Now        func() time.Time
	Log        *slog.Logger
	TrustProxy bool
}

// Handler serves <sub path>/app/<name>.
type Handler struct {
	d Deps
	// tariffID is the tariff the app sells (MIKAN_AOI_APP_TARIFF, «Стандарт» by default).
	tariffID int64
	// returnURL is where the buyer lands after paying (MIKAN_AOI_APP_RETURN_URL).
	returnURL string
	// proxies may say who the client is (X-Forwarded-For): MIKAN_AOI_APP_PROXY, comma-separated.
	proxies []string

	schemaMu sync.Mutex
	schemaOK bool

	payLimit   limiter
	trialLimit limiter
	orderCheck throttle
}

const (
	defaultTariff    = 3
	defaultReturnURL = "https://vpn.aoimusic.ru/app-pay-return.html"
	maxTokens        = 10
)

var (
	appIDRe   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	orderIDRe = appIDRe
	tokenRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)
)

func New(d Deps) *Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	h := &Handler{d: d, tariffID: defaultTariff, returnURL: defaultReturnURL,
		payLimit: limiter{max: 10, window: time.Minute}, trialLimit: limiter{max: 3, window: time.Minute}}
	if v, err := strconv.ParseInt(os.Getenv("MIKAN_AOI_APP_TARIFF"), 10, 64); err == nil && v > 0 {
		h.tariffID = v
	}
	h.proxies = []string{"79.137.198.59"}
	if v := strings.TrimSpace(os.Getenv("MIKAN_AOI_APP_PROXY")); v != "" {
		h.proxies = strings.Split(strings.ReplaceAll(v, " ", ""), ",")
	}
	if v := strings.TrimSpace(os.Getenv("MIKAN_AOI_APP_RETURN_URL")); strings.HasPrefix(v, "https://") {
		h.returnURL = v
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := h.ensureSchema(r.Context()); err != nil {
		h.d.Log.Error("aoiapp: schema", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "internal"})
		return
	}
	name := strings.Trim(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && name == "plans":
		h.plans(w, r)
	case r.Method == http.MethodGet && name == "pay":
		h.pay(w, r)
	case r.Method == http.MethodGet && name == "subscription":
		h.subscription(w, r)
	case r.Method == http.MethodGet && name == "subscriptions":
		h.subscriptions(w, r)
	case r.Method == http.MethodGet && name == "order":
		h.order(w, r)
	case r.Method == http.MethodPost && name == "core-report":
		h.coreReport(w, r)
	case r.Method == http.MethodPost && name == "test-sub":
		h.testSub(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not_found"})
	}
}

// ensureSchema makes the fork's tables once; a failure is tried again on the next request.
func (h *Handler) ensureSchema(ctx context.Context) error {
	h.schemaMu.Lock()
	defer h.schemaMu.Unlock()
	if h.schemaOK {
		return nil
	}
	if _, err := h.d.Store.DB.ExecContext(ctx, schema); err != nil {
		return err
	}
	h.schemaOK = true
	return nil
}

const schema = `
CREATE SEQUENCE IF NOT EXISTS aoi_app_tg_seq;
CREATE TABLE IF NOT EXISTS aoi_app_installs (
  app_id     TEXT PRIMARY KEY,
  tg_id      BIGINT NOT NULL UNIQUE, -- synthetic, negative: the install's "Telegram account" in billing
  created_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS aoi_app_orders (
  order_id        TEXT PRIMARY KEY,
  app_id          TEXT NOT NULL,
  payment_id      BIGINT NOT NULL,
  kind            TEXT NOT NULL,      -- new | renew
  user_id         BIGINT,             -- renew: the subscription renewed
  prev_expires_at BIGINT,             -- renew: its term before the payment
  created_at      BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS aoi_trial_keys (
  key        TEXT PRIMARY KEY,        -- ip:<addr> | fp:<fingerprint>
  tg_id      BIGINT NOT NULL,
  created_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS aoi_core_reports (
  id           BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  created_at   BIGINT NOT NULL,
  app_id       TEXT NOT NULL DEFAULT '',
  reason       TEXT NOT NULL DEFAULT '',
  core_version TEXT NOT NULL DEFAULT '',
  app_version  TEXT NOT NULL DEFAULT '',
  client_ip    TEXT NOT NULL DEFAULT '',
  log_tail     TEXT NOT NULL DEFAULT ''
);
`

// syntheticBase keeps the panel's synthetic accounts apart from the legacy bot's (about
// -5.5e9, carried over as they were) and from real Telegram users (positive).
const syntheticBase = 7_000_000_000

// newSyntheticTg is a fresh synthetic tg_id.
func (h *Handler) newSyntheticTg(ctx context.Context) (int64, error) {
	var n int64
	if err := h.d.Store.DB.QueryRowContext(ctx, "SELECT nextval('aoi_app_tg_seq')").Scan(&n); err != nil {
		return 0, err
	}
	return -(syntheticBase + n), nil
}

// installTg is the install's synthetic tg_id; create makes one when it has none.
func (h *Handler) installTg(ctx context.Context, appID string, create bool) (int64, bool, error) {
	var tg int64
	err := h.d.Store.DB.QueryRowContext(ctx, "SELECT tg_id FROM aoi_app_installs WHERE app_id = $1", appID).Scan(&tg)
	switch {
	case err == nil:
		return tg, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, err
	case !create:
		return 0, false, nil
	}
	if tg, err = h.newSyntheticTg(ctx); err != nil {
		return 0, false, err
	}
	// Two first requests at once: one insert wins, both read it back.
	if _, err := h.d.Store.DB.ExecContext(ctx,
		"INSERT INTO aoi_app_installs (app_id, tg_id, created_at) VALUES ($1, $2, $3) ON CONFLICT (app_id) DO NOTHING",
		appID, tg, h.d.Now().Unix()); err != nil {
		return 0, false, err
	}
	err = h.d.Store.DB.QueryRowContext(ctx, "SELECT tg_id FROM aoi_app_installs WHERE app_id = $1", appID).Scan(&tg)
	return tg, err == nil, err
}

func validAppID(s string) bool { return appIDRe.MatchString(s) }

// normalizeToken is the token in what the client sent: a bare token or the last segment
// of a subscription URL, without an extension.
func normalizeToken(raw string) string {
	t := strings.TrimSpace(raw)
	if i := strings.IndexByte(t, '?'); i >= 0 {
		t = t[:i]
	}
	if strings.Contains(t, "/") {
		t = strings.TrimRight(t, "/")
		t = t[strings.LastIndexByte(t, '/')+1:]
	}
	for _, ext := range []string{".sub", ".yaml", ".yml", ".json", ".txt"} {
		if strings.HasSuffix(t, ext) {
			t = strings.TrimSuffix(t, ext)
			break
		}
	}
	if !tokenRe.MatchString(t) {
		return ""
	}
	return t
}
