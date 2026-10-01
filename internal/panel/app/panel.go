package app

import (
	"context"
	"encoding/json"
	"errors"
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
	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/nodesync"
	"mikan/internal/panel/server"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/subs"
	"mikan/internal/panel/tgbot"
	"mikan/internal/panel/tlscert"
	"mikan/internal/panel/updates"
	"mikan/internal/panel/warp"
)

// Panel is the fully wired HTTP side of the panel, without the listener.
type Panel struct {
	Handler   http.Handler
	Settings  *settings.Settings
	Nodes     *nodesync.Manager
	Tuner     *autotune.Tuner // nil without nodes
	Telegram  *tgbot.Bot
	Billing   *billing.Service
	Updates   *updates.Checker
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
	// NodeCerts keeps the nodes' own certificates; nil: nodes have none.
	NodeCerts *tlscert.NodeStore
	// Certs manages the panel's public certificate; nil in development.
	Certs *acme.Manager
	// Autotune are the automatic moves' timings; zero means autotune.DefaultOptions.
	Autotune autotune.Options
	// TelegramAPI is the Bot API; "" is Telegram's.
	TelegramAPI string
	// SubPort moves the subscription port (0: none) and SubPortError says why the saved
	// one is not served; nil where the panel runs no server (tests, the CLI).
	SubPort      func(port int) error
	SubPortError func() string
	// DataDir is where the host updater and the panel meet (update/); "" turns that off.
	DataDir string
	// Releases fetches the newest release; nil never checks.
	Releases updates.Source
	// Payment providers' APIs; "" are the real ones (tests point them at fakes).
	YooKassaAPI, CryptoBotAPI string
	// WarpAPI is Cloudflare's WARP client API; "" is the real one.
	WarpAPI string
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
	deps.SubPort, deps.SubPortError = o.SubPort, o.SubPortError
	deps.Changes = changes
	deps.Users = domain.NewUsers(st, pool, changes, o.Now)
	deps.Devices = domain.NewDevices(st, pool, changes, o.Now)
	if o.Certs != nil {
		deps.Cert, deps.RenewCert = o.Certs.Status, o.Certs.Renew
		deps.SetCert, deps.ClearCert = o.Certs.SetCustom, o.Certs.ClearCustom
	}
	deps.NodeCerts = o.NodeCerts
	subBase := func(ctx context.Context) string {
		ep, err := set.SubEndpoint(ctx)
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
	p.Billing = billing.New(billing.Deps{Store: st, Settings: set, Users: deps.Users, Log: o.Log, Now: o.Now, TrustProxy: o.TrustProxy,
		YooKassaAPI: o.YooKassaAPI, CryptoBotAPI: o.CryptoBotAPI, CryptoBotTestAPI: o.CryptoBotAPI, MaxLinks: tgbot.MaxLinks})
	deps.Billing, deps.SubBase = p.Billing, subBase
	// The bot may reach Telegram through a node when the panel's server cannot.
	var tunnel func(ctx context.Context, nodeID int64, addr string) (net.Conn, error)
	if p.Nodes != nil {
		tunnel = p.Nodes.Tunnel
	}
	p.Telegram = tgbot.New(tgbot.Deps{Store: st, Settings: set, Devices: deps.Devices, SubBase: subBase, API: o.TelegramAPI, Log: o.Log, Now: o.Now, Billing: p.Billing,
		// Telegram apps refuse a Mini App on a self-signed certificate.
		MiniApp: func() bool { return o.Certs != nil && o.Certs.Status().Kind == "letsencrypt" }, Tunnel: tunnel})
	deps.Telegram = p.Telegram
	p.Billing.SetTelegram(p.Telegram)
	p.Updates = updates.New(o.DataDir, o.Version, o.Releases, o.Log, o.Now)
	deps.Updates = p.Updates
	deps.Warp = warp.Client{API: o.WarpAPI}
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
		lang, err := set.Lang(ctx)
		if err != nil {
			return subs.Config{}, err
		}
		var domainName, publicHost, routing, fingerprint, rules string
		var groups subs.Groups
		for key, dst := range map[string]*string{settings.KeyDomain: &domainName, settings.KeyPublicHost: &publicHost, settings.KeyGroupMain: &groups.Main, settings.KeyGroupAuto: &groups.Auto, settings.KeyGroupIcon: &groups.Icon,
			settings.KeyRouting: &routing, settings.KeyFingerprint: &fingerprint, settings.KeyRules: &rules} {
			if *dst, err = set.String(ctx, key); err != nil {
				return subs.Config{}, err
			}
		}
		// AoiVPN fork: external bypass proxies (sub_bypass setting, JSON) injected as a 2nd group.
		var bypass *subs.Bypass
		if raw, _ := set.String(ctx, settings.KeySubBypass); raw != "" {
			var bp subs.Bypass
			if json.Unmarshal([]byte(raw), &bp) == nil && bp.Group != "" && len(bp.Proxies) > 0 {
				bypass = &bp
			}
		}
		// AoiVPN fork: custom display order (sub_sort setting, JSON {nodes,presets}).
		var nodeOrder, protoOrder []string
		if raw, _ := set.String(ctx, settings.KeySubSort); raw != "" {
			var so struct {
				Nodes   []string `json:"nodes"`
				Presets []string `json:"presets"`
			}
			if json.Unmarshal([]byte(raw), &so) == nil {
				nodeOrder, protoOrder = so.Nodes, so.Presets
			}
		}
		cfg := subs.Config{Brand: brand, SupportURL: support, Groups: groups, Routing: subs.ParseRouting(routing), Fingerprint: fingerprint,
			Direct: []string{publicHost, domainName}, Lang: lang, Rules: subs.ServedRules(rules, groups.WithDefaults(lang)), Bypass: bypass,
			NodeOrder: nodeOrder, ProtoOrder: protoOrder}
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
			// AoiVPN fork: the local node is reached by its IP, not the panel's domain.
			// A .ru domain host (se.aoimusic.ru) is resolved client-side via DoH, which
			// stalls/gets poisoned under RU TSPU, so the whole node dies while IP-addressed
			// nodes work. The cert SNI stays the domain (hysteria/tuic match the LE cert);
			// reality carries its own SNI from the inbound.
			localHost := ep.Host
			if publicHost != "" {
				localHost = publicHost
			}
			sn := subs.Node{ID: n.ID, Name: n.Name, Endpoint: subs.Endpoint{Host: localHost, SNI: domainName}}
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
	subHandler.SetShop(p.Billing)

	adminMux := http.NewServeMux()
	adminMux.Handle("/api/", apiHandler)
	adminMux.Handle("/", p.spa)
	p.server = server.New(adminMux, subHandler)
	p.Handler = p.server
	return p, nil
}

// SubOnly is what the subscription port serves: the subscription path alone.
func (p *Panel) SubOnly() http.Handler { return p.server.SubOnly() }

// Apply loads the secret paths into the router and the default language into the pages.
func (p *Panel) Apply(ctx context.Context) (settings.Paths, error) {
	paths, err := p.Settings.Paths(ctx)
	if err != nil {
		return paths, err
	}
	lang, err := p.Settings.Lang(ctx)
	if err != nil {
		return paths, err
	}
	p.server.SetPaths(paths)
	p.spa.SetPrefix(paths.Admin)
	p.spa.SetLang(lang)
	if p.subPage != nil {
		p.subPage.SetPrefix(paths.Sub)
		p.subPage.SetLang(lang)
	}
	return paths, nil
}

// Run keeps paths and the language in sync with the DB (the CLI and the settings page
// edit them), drives the node syncer and cleans up expired state.
func (p *Panel) Run(ctx context.Context) {
	if p.Nodes != nil {
		go p.Nodes.Run(ctx)
	}
	if p.Tuner != nil {
		go p.Tuner.Run(ctx)
	}
	go p.Telegram.Run(ctx)
	go p.Billing.Run(ctx)
	// The host reads the switch from a file; the setting is what the admin chose.
	if auto, err := p.Settings.Bool(ctx, settings.KeyAutoUpdate, false); err == nil {
		if err := p.Updates.SetAuto(auto); err != nil && !errors.Is(err, updates.ErrUnavailable) {
			p.log.Error("update policy", "err", err)
		}
	}
	go p.Updates.Run(ctx)
	go every(ctx, 5*time.Second, func() {
		if _, err := p.Apply(ctx); err != nil {
			p.log.Error("reload settings", "err", err)
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
