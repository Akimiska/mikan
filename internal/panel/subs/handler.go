package subs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/server"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

// Config is resolved per request, so settings changes apply without a restart.
type Config struct {
	Brand      string
	SupportURL string
	Nodes      []Node   // enabled nodes in display order
	Direct     []string // the panel's and nodes' hosts: kept out of the tunnel
	Groups     Groups
	Routing    Routing
	// Fingerprint is the default uTLS profile for inbounds that set none.
	Fingerprint string
	// Binding gives every device that sends its id keys of its own (domain.Devices);
	// RequireHWID refuses apps that send none instead of seating them together.
	Binding     bool
	RequireHWID bool
	// Lang is the panel's default language, "" when unset: default group names and the
	// notices in place of servers are in it.
	Lang string
}

// Binder hands devices their keys (domain.Devices).
type Binder interface {
	Bind(ctx context.Context, u db.User, in domain.DeviceInfo, requireHWID bool) (db.Slot, error)
	Unbind(ctx context.Context, userID, deviceID int64, bySubscriber bool) error
}

type Handler struct {
	st      *store.Store
	cfg     func(ctx context.Context) (Config, error)
	page    http.Handler // browser page (SPA entry); nil = plain links
	now     func() time.Time
	devices Binder
	// trustProxy takes the client's IP from X-Forwarded-For (a reverse proxy in front).
	trustProxy bool
	tg         Telegram         // nil: no bot
	shop       *billing.Service // nil: nothing on sale
}

// Telegram is the bot's part in subscriptions: the page's "Open in Telegram" link and the
// Mini App's sign-in.
type Telegram interface {
	LinkURL(ctx context.Context, userID int64) string
	MiniAppUser(ctx context.Context, initData string) (int64, []db.User, error)
}

// SetTelegram plugs in the bot.
func (h *Handler) SetTelegram(tg Telegram) { h.tg = tg }

// SetShop takes payments: the providers' webhooks under /pay/ and the Mini App's shop.
func (h *Handler) SetShop(s *billing.Service) { h.shop = s }

func NewHandler(st *store.Store, cfg func(ctx context.Context) (Config, error), page http.Handler, now func() time.Time, devices Binder, trustProxy bool) *Handler {
	return &Handler{st: st, cfg: cfg, page: page, now: now, devices: devices, trustProxy: trustProxy}
}

// clientIP is the device's address as the nodes see it too: clients reach the panel
// directly (its host is a DIRECT rule in the profile).
func (h *Handler) clientIP(r *http.Request) string {
	if h.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		return ip.String()
	}
	return host
}

var unbindPath = regexp.MustCompile(`^devices/([0-9]{1,18})/unbind$`)

// ServeHTTP handles "/<token>", "/<token>/info", "POST /<token>/devices/<id>/unbind" (the
// subscription page) and the page assets under the sub prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	token, rest, _ := strings.Cut(p, "/")
	if token == "tg" && h.tg != nil {
		h.miniApp(w, r, rest)
		return
	}
	if token == "pay" && h.shop != nil {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + rest
		h.shop.Webhook().ServeHTTP(w, r2)
		return
	}
	unbind := unbindPath.FindStringSubmatch(rest)
	switch {
	case r.Method == http.MethodPost && unbind != nil:
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		server.NotFound(w)
		return
	case (strings.HasPrefix(p, "assets/") || p == "favicon.svg") && h.page != nil:
		h.page.ServeHTTP(w, r)
		return
	case rest != "" && rest != "info":
		server.NotFound(w)
		return
	}
	if len(token) != 24 {
		server.NotFound(w)
		return
	}
	u, err := h.st.Q.GetUserBySubToken(r.Context(), token)
	if err != nil {
		server.NotFound(w)
		return
	}
	if unbind != nil {
		id, _ := strconv.ParseInt(unbind[1], 10, 64)
		h.unbind(w, r, u, id)
		return
	}
	cfg, err := h.cfg(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rest == "info" {
		prof, err := h.profile(r.Context(), u, cfg, db.Slot{})
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.info(r.Context(), w, u, prof, cfg)
		return
	}
	format := Format(r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.URL.Query().Get("format"))
	if format == "html" && h.page != nil {
		h.page.ServeHTTP(w, r)
		return
	}
	h.userInfoHeaders(w, u, cfg)
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return // apps peek at the traffic headers; the keys go only with a real fetch
	}
	slot, err := h.slotFor(r, u, cfg)
	switch {
	case errors.Is(err, domain.ErrDeviceLimit), errors.Is(err, domain.ErrNoHWID):
		h.stub(w, u, cfg, format, err)
		return
	case err != nil:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	prof, err := h.profile(r.Context(), u, cfg, slot)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	prof.Inbounds = forApp(prof.Inbounds, DetectApp(r.Header.Get("User-Agent")), domain.State(u, h.now()))
	// The block detector trusts a device only once it took a profile with an inbound's
	// current port and target (see autotune.Detect).
	_ = h.st.Q.RecordSubFetch(r.Context(), db.RecordSubFetchParams{UserID: u.ID, Ip: h.clientIP(r), FetchedAt: h.now().Unix()})
	switch format {
	case "clash":
		body, err := Mihomo(prof, cfg.Groups.WithDefaults(cfg.Lang), cfg.Routing)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(cfg.Brand)+".yaml")
		_, _ = w.Write(body)
	default:
		links, err := URIs(prof)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(links))))
	}
}

// miniApp serves the subscription page inside Telegram and signs its user in.
func (h *Handler) miniApp(w http.ResponseWriter, r *http.Request, rest string) {
	switch {
	case r.Method == http.MethodGet && rest == "" && h.page != nil:
		// Telegram Web opens Mini Apps in a frame; the apps open them in a web view.
		hd := w.Header()
		hd.Set("Content-Security-Policy", strings.Replace(hd.Get("Content-Security-Policy"), "frame-ancestors 'none'", "frame-ancestors https://web.telegram.org", 1))
		hd.Del("X-Frame-Options")
		h.page.ServeHTTP(w, r)
	case r.Method == http.MethodPost && rest == "session" && sameOrigin(r):
		var in struct {
			InitData string `json:"init_data"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, users, err := h.tg.MiniAppUser(r.Context(), in.InitData)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "init_data"})
			return
		}
		type sub struct {
			Token string `json:"token"`
			Name  string `json:"name"`
		}
		out := struct {
			Subs []sub `json:"subs"`
		}{Subs: []sub{}}
		for _, u := range users {
			out.Subs = append(out.Subs, sub{Token: u.SubToken, Name: u.Name})
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost && (rest == "shop" || rest == "pay") && sameOrigin(r) && h.shop != nil:
		h.miniAppShop(w, r, rest)
	default:
		server.NotFound(w)
	}
}

// miniAppShop: "shop" lists what the Telegram account can buy, "pay" opens an invoice
// for a new subscription (token "") or one of the account's own.
func (h *Handler) miniAppShop(w http.ResponseWriter, r *http.Request, rest string) {
	var in struct {
		InitData string `json:"init_data"`
		TariffID int64  `json:"tariff_id"`
		Provider string `json:"provider"`
		Token    string `json:"token"`
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
	}
	if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in) != nil {
		fail(http.StatusBadRequest, "bad_request")
		return
	}
	tgID, users, err := h.tg.MiniAppUser(r.Context(), in.InitData)
	if err != nil {
		fail(http.StatusUnauthorized, "init_data")
		return
	}
	ctx := r.Context()
	if rest == "shop" {
		offers, av, err := h.shop.Offers(ctx)
		if err != nil {
			fail(http.StatusInternalServerError, "internal")
			return
		}
		cfg, _ := h.cfg(ctx)
		type offer struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Stars       int64  `json:"stars,omitempty"`
			Rub         int64  `json:"rub,omitempty"`
		}
		out := struct {
			AllowNew  bool            `json:"allow_new"`
			Providers map[string]bool `json:"providers"`
			Offers    []offer         `json:"offers"`
		}{AllowNew: h.shop.Config(ctx).AllowNew, Offers: []offer{},
			Providers: map[string]bool{billing.Stars: av.Stars, billing.YooKassa: av.YooKassa, billing.CryptoBot: av.CryptoBot}}
		for _, o := range offers {
			out.Offers = append(out.Offers, offer{ID: o.Tariff.ID, Name: o.Tariff.Name, Description: billing.Describe(o.Tariff, cfg.Lang), Stars: o.Stars, Rub: o.Rub})
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	var userID int64
	if in.Token != "" {
		for _, u := range users {
			if u.SubToken == in.Token {
				userID = u.ID
			}
		}
		if userID == 0 {
			fail(http.StatusForbidden, billing.ErrNotYours.Error())
			return
		}
	}
	p, err := h.shop.Invoice(ctx, billing.InvoiceRequest{TgID: tgID, UserID: userID, TariffID: in.TariffID, Provider: in.Provider})
	if err != nil {
		code := billing.ErrProviderOff.Error()
		for _, e := range []error{billing.ErrNotForSale, billing.ErrNewOff, billing.ErrTooManySubs, billing.ErrTooMany, billing.ErrNotYours} {
			if errors.Is(err, e) {
				code = e.Error()
			}
		}
		fail(http.StatusConflict, code)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"url": p.PayUrl, "provider": p.Provider})
}

// forApp keeps the inbounds the app can use. Inbounds with one key for everyone go only
// to users whose access is on: the node cannot cut anyone off there.
func forApp(ins []db.Inbound, app App, state string) []db.Inbound {
	active := domain.CanConnect(state)
	out := make([]db.Inbound, 0, len(ins))
	for _, in := range ins {
		t, err := proto.Parse(in.Config)
		if err != nil || !app.Supports(proto.NeedsOf(t)) || proto.Shared(t.Type()) && !active {
			continue
		}
		out = append(out, in)
	}
	return out
}

// slotFor is whose keys this request gets: its device's own with binding on, the user's
// otherwise.
func (h *Handler) slotFor(r *http.Request, u db.User, cfg Config) (db.Slot, error) {
	if !cfg.Binding || h.devices == nil {
		if !u.SlotID.Valid {
			return db.Slot{}, errors.New("user has no slot")
		}
		return h.st.Q.GetSlot(r.Context(), u.SlotID.Int64)
	}
	return h.devices.Bind(r.Context(), u, domain.DeviceInfo{
		HWID: r.Header.Get("X-Hwid"), OS: r.Header.Get("X-Device-Os"), OSVersion: r.Header.Get("X-Ver-Os"),
		Model: r.Header.Get("X-Device-Model"), App: r.Header.Get("User-Agent"), IP: h.clientIP(r),
	}, cfg.RequireHWID)
}

// profile lists what the user may use; slot is whose keys go in (zero for the page).
func (h *Handler) profile(ctx context.Context, u db.User, cfg Config, slot db.Slot) (Profile, error) {
	prof := Profile{Nodes: cfg.Nodes, Direct: cfg.Direct, Slot: slot, Fingerprint: cfg.Fingerprint}
	all, err := h.st.Q.ListInbounds(ctx)
	if err != nil {
		return prof, err
	}
	allowed := domain.DecodeInbounds(u.Inbounds)
	nodes := map[int64]bool{}
	for _, n := range cfg.Nodes {
		nodes[n.ID] = true
	}
	for _, in := range all {
		if in.Enabled == 0 || !nodes[in.NodeID] || (len(allowed) > 0 && !slices.Contains(allowed, in.ID)) {
			continue
		}
		prof.Inbounds = append(prof.Inbounds, in)
	}
	return prof, nil
}

func (h *Handler) userInfoHeaders(w http.ResponseWriter, u db.User, cfg Config) {
	var total, expire int64
	if u.TrafficLimit.Valid {
		total = u.TrafficLimit.Int64
	}
	if u.ExpiresAt.Valid {
		expire = u.ExpiresAt.Int64
	}
	hd := w.Header()
	hd.Set("Subscription-Userinfo", "upload="+strconv.FormatInt(u.UsedUp, 10)+"; download="+strconv.FormatInt(u.UsedDown, 10)+
		"; total="+strconv.FormatInt(total, 10)+"; expire="+strconv.FormatInt(expire, 10))
	// Hourly: a port or target the panel changed on its own reaches clients soon.
	hd.Set("Profile-Update-Interval", "1")
	hd.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(cfg.Brand)))
	if cfg.SupportURL != "" {
		hd.Set("Support-Url", cfg.SupportURL)
	}
}

// Info is what the subscription page shows. Credentials are not included: the page
// offers import buttons that point back at this subscription URL.
type Info struct {
	Name       string     `json:"name"`
	Brand      string     `json:"brand"`
	SupportURL string     `json:"support_url,omitempty"`
	State      string     `json:"state"`
	UsedUp     int64      `json:"used_up"`
	UsedDown   int64      `json:"used_down"`
	Limit      *int64     `json:"limit,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	ResetsAt   *time.Time `json:"resets_at,omitempty"`
	Devices    int        `json:"device_limit"`
	Protocols  []string   `json:"protocols"`
	Locations  []string   `json:"locations,omitempty"`
	// Telegram opens the bot with this subscription tied to the account; empty without a bot.
	Telegram string `json:"telegram,omitempty"`
	// Bound devices, when binding is on. The device id itself stays in the admin panel.
	Binding     bool         `json:"binding"`
	Bound       []DeviceItem `json:"devices"`
	UnbindAfter *time.Time   `json:"unbind_after,omitempty" doc:"The subscriber may unbind again from then"`
}

// DeviceItem is a bound device as the subscription page lists it.
type DeviceItem struct {
	ID        int64     `json:"id"`
	OS        string    `json:"os"`
	OSVersion string    `json:"os_version"`
	Model     string    `json:"model"`
	App       string    `json:"app"`
	Shared    bool      `json:"shared" doc:"Apps that send no device id, seated together"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

func (h *Handler) info(ctx context.Context, w http.ResponseWriter, u db.User, prof Profile, cfg Config) {
	now := h.now()
	out := Info{Name: u.Name, Brand: cfg.Brand, SupportURL: cfg.SupportURL, State: domain.State(u, now), UsedUp: u.UsedUp, UsedDown: u.UsedDown,
		Binding: cfg.Binding, Bound: []DeviceItem{}}
	if h.tg != nil {
		out.Telegram = h.tg.LinkURL(ctx, u.ID)
	}
	if cfg.Binding {
		devs, err := h.st.Q.ListBoundDevices(ctx, u.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		for _, d := range devs {
			out.Bound = append(out.Bound, DeviceItem{ID: d.ID, OS: d.Os, OSVersion: d.OsVersion, Model: d.Model, App: d.App, Shared: d.Hwid == "",
				CreatedAt: time.Unix(d.CreatedAt, 0).UTC(), LastSeen: time.Unix(d.LastSeen, 0).UTC()})
		}
		if t := domain.NextUnbind(u, now); !t.IsZero() {
			out.UnbindAfter = &t
		}
	}
	if u.TrafficLimit.Valid {
		out.Limit = &u.TrafficLimit.Int64
	}
	if u.ExpiresAt.Valid {
		t := time.Unix(u.ExpiresAt.Int64, 0).UTC()
		out.ExpiresAt = &t
	}
	if t, ok := domain.NextReset(u, now); ok {
		out.ResetsAt = &t
	}
	if u.DeviceLimit.Valid {
		out.Devices = int(u.DeviceLimit.Int64)
	}
	onNode := map[int64]bool{}
	for _, in := range prof.Inbounds {
		onNode[in.NodeID] = true
		if !slices.Contains(out.Protocols, in.Preset) {
			out.Protocols = append(out.Protocols, in.Preset)
		}
	}
	for _, n := range cfg.Nodes {
		if onNode[n.ID] && n.Name != "" {
			out.Locations = append(out.Locations, n.Name)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// unbind frees a device's place from the subscription page. Only the page itself may do
// it (a POST from another site is refused), and only once a day.
func (h *Handler) unbind(w http.ResponseWriter, r *http.Request, u db.User, id int64) {
	if !sameOrigin(r) || h.devices == nil {
		server.NotFound(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	err := h.devices.Unbind(r.Context(), u.ID, id, true)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, domain.ErrUnbindCooldown):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "unbind_cooldown", "unbind_after": domain.NextUnbind(u, h.now())})
	case errors.Is(err, domain.ErrNotFound):
		server.NotFound(w)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// sameOrigin: the browser says the request comes from this very site.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
		o, err := url.Parse(r.Header.Get("Origin"))
		return err == nil && o.Host != "" && o.Host == r.Host
	}
	return false
}

// stub answers a device that gets no keys: one placeholder server named after the reason,
// so the app shows it where the servers would be, and the headers Happ-like apps read.
func (h *Handler) stub(w http.ResponseWriter, u db.User, cfg Config, format string, reason error) {
	en := cfg.Lang == "en"
	name := "⛔ Все места для устройств заняты — откройте ссылку подписки в браузере"
	if en {
		name = "⛔ All device places are taken — open the subscription link in a browser"
	}
	if errors.Is(reason, domain.ErrNoHWID) {
		name = "⛔ Приложение не сообщает ID устройства — поставьте Happ, Koala Clash или INCY"
		if en {
			name = "⛔ The app does not send a device ID — install Happ, Koala Clash or INCY"
		}
		w.Header().Set("X-Hwid-Not-Supported", "true")
	} else {
		w.Header().Set("X-Hwid-Max-Devices-Reached", "true")
	}
	if format == "clash" {
		main := cfg.Groups.WithDefaults(cfg.Lang).Main
		// JSON is YAML: the same as the real profile (see Mihomo).
		body, _ := json.Marshal(map[string]any{
			"proxies":      []map[string]any{{"name": name, "type": "socks5", "server": "127.0.0.1", "port": 1}},
			"proxy-groups": []map[string]any{{"name": main, "type": "select", "proxies": []string{name}}},
			"rules":        []string{"MATCH," + main},
		})
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		_, _ = w.Write(body)
		return
	}
	link := "vless://00000000-0000-0000-0000-000000000000@127.0.0.1:1?encryption=none&type=tcp#" + url.PathEscape(name)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(link))))
}

var (
	clashAgents = []string{"clash", "mihomo", "flclash", "stash", "verge", "koala"}
	uriAgents   = []string{"happ", "v2raytun", "v2rayng", "v2rayn", "streisand", "hiddify", "nekobox", "nekoray", "karing", "shadowrocket", "foxray", "v2box", "sing-box"}
)

// Format picks the response format: explicit ?format= wins, then the client's User-Agent.
func Format(userAgent, accept, query string) string {
	switch strings.ToLower(query) {
	case "clash", "mihomo", "yaml":
		return "clash"
	case "uri", "v2ray", "base64":
		return "uri"
	case "html":
		return "html"
	}
	ua := strings.ToLower(userAgent)
	for _, a := range clashAgents {
		if strings.Contains(ua, a) {
			return "clash"
		}
	}
	for _, a := range uriAgents {
		if strings.Contains(ua, a) {
			return "uri"
		}
	}
	if strings.Contains(accept, "text/html") {
		return "html"
	}
	return "uri"
}
