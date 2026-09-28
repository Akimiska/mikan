package app

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/acme"
	"mikan/internal/panel/api"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/nodesync"
	"mikan/internal/panel/server"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/subs"
)

// Panel is the fully wired HTTP side of the panel, without the listener.
type Panel struct {
	Handler   http.Handler
	Settings  *settings.Settings
	Syncer    *nodesync.Syncer
	server    *server.Server
	spa       *server.SPA
	subPage   *server.SPA
	sessions  *auth.Sessions
	ipLimit   *auth.Limiter
	userLimit *auth.Limiter
	now       func() time.Time
	log       *slog.Logger
}

type Options struct {
	Version    string
	Web        fs.FS
	TrustProxy bool
	Log        *slog.Logger
	Now        func() time.Time
	// Node is nil when the panel runs without a node (tests, UI development).
	Node nodesync.Node
	// TLS provides the certificate shared with the node and its pin for self-signed setups.
	TLS func() (*nodeapi.TLSFiles, string, error)
	// Certs manages the panel's public certificate; nil in development.
	Certs *acme.Manager
}

type noChanges struct{}

func (noChanges) PoliciesChanged() {}
func (noChanges) SlotsChanged()    {}

func NewPanel(st *store.Store, o Options) (*Panel, error) {
	set := settings.New(st.Q)
	p := &Panel{
		Settings:  set,
		sessions:  auth.NewSessions(st.Q, o.Now),
		ipLimit:   auth.NewLimiter(10, 10*time.Minute, 15*time.Minute, 24*time.Hour),
		userLimit: auth.NewLimiter(30, 10*time.Minute, 15*time.Minute, 24*time.Hour),
		now:       o.Now,
		log:       o.Log,
	}
	pool := domain.NewPool(st, o.Now)
	var changes domain.Changes = noChanges{}
	deps := api.Deps{
		Version: o.Version, Store: st, Settings: set, Sessions: p.sessions,
		IPLimit: p.ipLimit, UserLimit: p.userLimit, TOTP: auth.NewTOTPGuard(),
		TrustProxy: o.TrustProxy, Log: o.Log, Now: o.Now, Pool: pool,
	}
	if o.Node != nil {
		tlsFiles := func() (*nodeapi.TLSFiles, error) {
			f, _, err := o.TLS()
			return f, err
		}
		p.Syncer = nodesync.New(st, set, pool, o.Node, tlsFiles, o.Log, o.Now)
		changes = p.Syncer
		deps.Online = p.Syncer.Online
		deps.Health = p.Syncer.Health
		deps.Listeners = func() []nodeapi.ListenerStatus { return p.Syncer.Health().Listeners }
	}
	deps.Changes = changes
	deps.Users = domain.NewUsers(st, pool, changes, o.Now)
	if o.Certs != nil {
		deps.Cert, deps.RenewCert = o.Certs.Status, o.Certs.Renew
	}
	deps.SubURL = func(ctx context.Context, token string) string {
		ep, err := set.Endpoint(ctx)
		if err != nil || ep.Host == "" {
			return ""
		}
		paths, err := set.Paths(ctx)
		if err != nil {
			return ""
		}
		return "https://" + net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port)) + "/" + paths.Sub + "/" + token
	}
	apiHandler, _, err := api.New(deps)
	if err != nil {
		return nil, err
	}
	p.spa, err = server.NewSPA(o.Web, "index.html")
	if err != nil {
		return nil, fmt.Errorf("web bundle: %w", err)
	}
	var subPageHandler http.Handler
	if sp, err := server.NewSPA(o.Web, "sub.html"); err == nil {
		p.subPage, subPageHandler = sp, sp
	}
	subCfg := func(ctx context.Context) (subs.Config, error) {
		ep, err := set.Endpoint(ctx)
		if err != nil {
			return subs.Config{}, err
		}
		brand, _, err := settings.Get[string](ctx, set, "brand")
		if err != nil {
			return subs.Config{}, err
		}
		if brand == "" {
			brand = "VPN"
		}
		support, _, err := settings.Get[string](ctx, set, "support_url")
		if err != nil {
			return subs.Config{}, err
		}
		domainName, err := set.String(ctx, settings.KeyDomain)
		if err != nil {
			return subs.Config{}, err
		}
		cfg := subs.Config{Brand: brand, SupportURL: support, Endpoint: subs.Endpoint{Host: ep.Host, SNI: domainName}, Rules: []string{"MATCH,VPN"}}
		if o.TLS != nil {
			if _, pin, err := o.TLS(); err == nil {
				cfg.Endpoint.PinSHA256 = pin
			}
		}
		return cfg, nil
	}
	subHandler := subs.NewHandler(st, subCfg, subPageHandler, o.Now)

	adminMux := http.NewServeMux()
	adminMux.Handle("/api/", apiHandler)
	adminMux.Handle("/", p.spa)
	p.server = server.New(adminMux, subHandler)
	p.Handler = p.server
	return p, nil
}

// ApplyPaths loads the secret paths from settings into the router.
func (p *Panel) ApplyPaths(ctx context.Context) (settings.Paths, error) {
	paths, err := p.Settings.Paths(ctx)
	if err != nil {
		return paths, err
	}
	p.server.SetPaths(paths)
	p.spa.SetPrefix(paths.Admin)
	if p.subPage != nil {
		p.subPage.SetPrefix(paths.Sub)
	}
	return paths, nil
}

// Run keeps paths in sync with the DB (the CLI edits them), drives the node syncer and
// cleans up expired state.
func (p *Panel) Run(ctx context.Context) {
	if p.Syncer != nil {
		go p.Syncer.Run(ctx)
	}
	go every(ctx, 5*time.Second, func() {
		if _, err := p.ApplyPaths(ctx); err != nil {
			p.log.Error("reload paths", "err", err)
		}
	})
	every(ctx, 10*time.Minute, func() {
		if err := p.sessions.Cleanup(ctx); err != nil {
			p.log.Error("session cleanup", "err", err)
		}
		p.ipLimit.Sweep(p.now())
		p.userLimit.Sweep(p.now())
	})
}

func every(ctx context.Context, d time.Duration, fn func()) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}
