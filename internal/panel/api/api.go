package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/acme"
	"mikan/internal/panel/audit"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/nodesync"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

type Deps struct {
	Version    string
	Store      *store.Store
	Settings   *settings.Settings
	Sessions   *auth.Sessions
	IPLimit    *auth.Limiter
	UserLimit  *auth.Limiter
	TOTP       *auth.TOTPGuard
	TrustProxy bool
	Log        *slog.Logger
	Now        func() time.Time

	Users     *domain.Users
	Pool      *domain.Pool
	Changes   domain.Changes
	SubURL    func(ctx context.Context, token string) string
	Online    func() map[string]nodeapi.Online
	Listeners func() []nodeapi.ListenerStatus
	Health    func() nodesync.HealthView
	Cert      func() acme.Status
	RenewCert func()
}

type ctxKey int

const (
	keyClient ctxKey = iota
	keySession
)

type client struct{ IP, UserAgent string }

type handlers struct {
	d         Deps
	api       huma.API
	dummyHash string

	pendingMu sync.Mutex
	pending   map[int64]pendingTOTP
}

// Config builds the huma config shared by the server and the `mikan openapi` command.
func Config(version string) huma.Config {
	// Handlers always return non-nil slices; nullable arrays would force null checks in the UI.
	huma.DefaultArrayNullable = false
	cfg := huma.DefaultConfig("mikan", version)
	// No docs UI, no spec endpoint and no $schema links at runtime: the spec is
	// exported by the CLI at build time for the TypeScript client.
	cfg.DocsPath = ""
	cfg.OpenAPIPath = ""
	cfg.SchemasPath = ""
	cfg.CreateHooks = nil
	return cfg
}

func New(d Deps) (http.Handler, huma.API, error) {
	mux := http.NewServeMux()
	api := humago.New(mux, Config(d.Version))
	dummy, err := auth.HashPassword(secure.Token(32))
	if err != nil {
		return nil, nil, err
	}
	h := &handlers{d: d, api: api, dummyHash: dummy, pending: map[int64]pendingTOTP{}}
	api.UseMiddleware(h.middleware)
	h.registerAuth()
	h.registerUsers()
	h.registerCatalog()
	h.registerStats()
	h.registerSettings()
	return noStore(mux), api, nil
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) middleware(ctx huma.Context, next func(huma.Context)) {
	ctx = huma.WithValue(ctx, keyClient, client{IP: h.clientIP(ctx), UserAgent: ctx.Header("User-Agent")})
	op := ctx.Operation()
	mutating := op.Method != http.MethodGet && op.Method != http.MethodHead
	if mutating && !sameOrigin(ctx) {
		_ = huma.WriteErr(h.api, ctx, http.StatusForbidden, "csrf")
		return
	}
	if public, _ := op.Metadata["public"].(bool); public {
		next(ctx)
		return
	}
	ck, err := huma.ReadCookie(ctx, auth.CookieName)
	if err != nil {
		_ = huma.WriteErr(h.api, ctx, http.StatusUnauthorized, "unauthorized")
		return
	}
	sess, err := h.d.Sessions.Lookup(ctx.Context(), ck.Value)
	if err != nil {
		if err != auth.ErrNoSession {
			h.d.Log.Error("session lookup", "err", err)
			_ = huma.WriteErr(h.api, ctx, http.StatusInternalServerError, "internal error")
			return
		}
		_ = huma.WriteErr(h.api, ctx, http.StatusUnauthorized, "unauthorized")
		return
	}
	if mutating && !secure.Equal(ctx.Header("X-CSRF-Token"), sess.CsrfToken) {
		_ = huma.WriteErr(h.api, ctx, http.StatusForbidden, "csrf")
		return
	}
	next(huma.WithValue(ctx, keySession, sess))
}

// sameOrigin rejects cross-site browser requests. Non-browser clients send neither
// Sec-Fetch-Site nor Origin; they still need the CSRF header for authenticated calls.
func sameOrigin(ctx huma.Context) bool {
	switch ctx.Header("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
	default:
		return false
	}
	origin := ctx.Header("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == ctx.Host()
}

func (h *handlers) clientIP(ctx huma.Context) string {
	if h.d.TrustProxy {
		if xff := ctx.Header("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(ctx.RemoteAddr())
	if err != nil {
		return ctx.RemoteAddr()
	}
	return host
}

func clientOf(ctx context.Context) client {
	c, _ := ctx.Value(keyClient).(client)
	return c
}

func sessionOf(ctx context.Context) db.Session {
	s, _ := ctx.Value(keySession).(db.Session)
	return s
}

func (h *handlers) audit(ctx context.Context, adminID int64, action, targetType, targetID string, details any) {
	err := audit.Write(ctx, h.d.Store.Q, h.d.Now(), audit.Entry{
		AdminID: adminID, Action: action, TargetType: targetType, TargetID: targetID,
		IP: clientOf(ctx).IP, Details: details,
	})
	if err != nil {
		h.d.Log.Warn("audit write failed", "action", action, "err", err)
	}
}
