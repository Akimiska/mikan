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
	"mikan/internal/nodetls"
	"mikan/internal/panel/acme"
	"mikan/internal/panel/api"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/autotune"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/nodesync"
	"mikan/internal/panel/server"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/subs"
	"mikan/internal/panel/tgbot"
)

// Panel is the fully wired HTTP side of the panel, without the listener.
type Panel struct {
	Handler   http.Handler
	Settings  *settings.Settings
	Nodes     *nodesync.Manager
	Tuner     *autotune.Tuner // nil without nodes
	Telegram  *tgbot.Bot
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
	// Connect reaches a node; nil when the panel runs without nodes (tests, UI development).
	Connect nodesync.Connect
	// QUIC is a node's long-lived self-signed Hysteria2/TUIC certificate and its pin,
	// which subscription links carry.
	QUIC func(n db.Node) (*nodeapi.TLSFiles, string, error)
	// PanelCert is the client certificate remote nodes pin; join keys carry its hash.
	PanelCert func() (nodetls.Pair, error)
	// Certs manages the panel's public certificate; nil in development.
	Certs *acme.Manager
	// Autotune are the automatic moves' timings; zero means autotune.DefaultOptions.
	Autotune autotune.Options
	// TelegramAPI is the Bot API; "" is Telegram's.
	TelegramAPI string
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
	if o.Connect != nil {
		p.Nodes = nodesync.NewManager(st, set, pool, o.Connect, o.Log, o.Now)
		changes = p.Nodes
		deps.Online = p.Nodes.Online
		deps.Nodes = p.Nodes
		tune := o.Autotune
		if tune == (autotune.Options{}) {
			tune = autotune.DefaultOptions()
		}
		p.Tuner = autotune.New(st, set, p.Nodes, p.Nodes, o.Log, o.Now, tune)
		deps.Tuner = p.Tuner
	}
	deps.PanelCert = o.PanelCert
	deps.Changes = changes
	deps.Users = domain.NewUsers(st, pool, changes, o.Now)
	deps.Devices = domain.NewDevices(st, pool, changes, o.Now)
	if o.Certs != nil {
		deps.Cert, deps.RenewCert = o.Certs.Status, o.Certs.Renew
	}
	subBase := func(ctx context.Context) string {
		ep, err := set.Endpoint(ctx)
		if err != nil || ep.Host == "" {
			return ""
		}
		paths, err := set.Paths(ctx)
		if err != nil {
			return ""
		}
		return "https://" + net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port)) + "/" + paths.Sub
	}
	deps.SubURL = func(ctx context.Context, token string) string {
		if base := subBase(ctx); base != "" {
			return base + "/" + token
		}
		return ""
	}
	p.Telegram = tgbot.New(tgbot.Deps{Store: st, Settings: set, Devices: deps.Devices, SubBase: subBase, API: o.TelegramAPI, Log: o.Log, Now: o.Now,
		// Telegram apps refuse a Mini App on a self-signed certificate.
		MiniApp: func() bool { return o.Certs != nil && o.Certs.Status().Kind == "letsencrypt" }})
	deps.Telegram = p.Telegram
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
		var domainName, publicHost, routing string
		var groups subs.Groups
		for key, dst := range map[string]*string{settings.KeyDomain: &domainName, settings.KeyPublicHost: &publicHost, settings.KeyGroupMain: &groups.Main, settings.KeyGroupAuto: &groups.Auto,
			settings.KeyRouting: &routing} {
			if *dst, err = set.String(ctx, key); err != nil {
				return subs.Config{}, err
			}
		}
		cfg := subs.Config{Brand: brand, SupportURL: support, Groups: groups, Routing: subs.ParseRouting(routing),
			Direct: []string{publicHost, domainName}}
		if cfg.Binding, err = set.Bool(ctx, settings.KeyDeviceBinding, true); err != nil {
			return subs.Config{}, err
		}
		if cfg.RequireHWID, err = set.Bool(ctx, settings.KeyRequireHWID, false); err != nil {
			return subs.Config{}, err
		}
		nodes, err := st.Q.ListNodes(ctx)
		if err != nil {
			return subs.Config{}, err
		}
		for _, n := range nodes {
			if n.Enabled == 0 {
				continue
			}
			sn := subs.Node{ID: n.ID, Name: n.Name, Endpoint: subs.Endpoint{Host: ep.Host, SNI: domainName}}
			if n.Address != "" {
				sn.Endpoint = subs.Endpoint{Host: domain.NodeHost(n), SNI: n.Domain}
				cfg.Direct = append(cfg.Direct, n.PublicHost, n.Domain)
			}
			if o.QUIC != nil {
				if _, pin, err := o.QUIC(n); err == nil {
					sn.Endpoint.PinSHA256 = pin
				}
			}
			cfg.Nodes = append(cfg.Nodes, sn)
		}
		return cfg, nil
	}
	subHandler := subs.NewHandler(st, subCfg, subPageHandler, o.Now, deps.Devices, o.TrustProxy)
	subHandler.SetTelegram(p.Telegram)

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
	if p.Nodes != nil {
		go p.Nodes.Run(ctx)
	}
	if p.Tuner != nil {
		go p.Tuner.Run(ctx)
	}
	go p.Telegram.Run(ctx)
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
