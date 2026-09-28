package subs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/server"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Config is resolved per request, so settings changes apply without a restart.
type Config struct {
	Brand      string
	SupportURL string
	Endpoint   Endpoint
	Rules      []string
}

type Handler struct {
	st   *store.Store
	cfg  func(ctx context.Context) (Config, error)
	page http.Handler // browser page (SPA entry); nil = plain links
	now  func() time.Time
}

func NewHandler(st *store.Store, cfg func(ctx context.Context) (Config, error), page http.Handler, now func() time.Time) *Handler {
	return &Handler{st: st, cfg: cfg, page: page, now: now}
}

// ServeHTTP handles "/<token>", "/<token>/info" and the page assets under the sub prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		server.NotFound(w)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if (strings.HasPrefix(p, "assets/") || p == "favicon.svg") && h.page != nil {
		h.page.ServeHTTP(w, r)
		return
	}
	token, rest, _ := strings.Cut(p, "/")
	if len(token) != 24 || (rest != "" && rest != "info") {
		server.NotFound(w)
		return
	}
	u, err := h.st.Q.GetUserBySubToken(r.Context(), token)
	if err != nil {
		server.NotFound(w)
		return
	}
	cfg, err := h.cfg(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	prof, err := h.profile(r.Context(), u, cfg.Endpoint)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rest == "info" {
		h.info(w, u, prof, cfg)
		return
	}
	format := Format(r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.URL.Query().Get("format"))
	if format == "html" && h.page != nil {
		h.page.ServeHTTP(w, r)
		return
	}
	h.userInfoHeaders(w, u, cfg)
	w.Header().Set("Cache-Control", "no-store")
	switch format {
	case "clash":
		body, err := Mihomo(prof, cfg.Rules)
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

func (h *Handler) profile(ctx context.Context, u db.User, ep Endpoint) (Profile, error) {
	prof := Profile{Endpoint: ep}
	if !u.SlotID.Valid {
		return prof, errors.New("user has no slot")
	}
	slot, err := h.st.Q.GetSlot(ctx, u.SlotID.Int64)
	if err != nil {
		return prof, err
	}
	prof.Slot = slot
	all, err := h.st.Q.ListInbounds(ctx)
	if err != nil {
		return prof, err
	}
	allowed := domain.DecodeInbounds(u.Inbounds)
	for _, in := range all {
		if in.Enabled == 0 || (len(allowed) > 0 && !slices.Contains(allowed, in.ID)) {
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
	hd.Set("Profile-Update-Interval", "12")
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
}

func (h *Handler) info(w http.ResponseWriter, u db.User, prof Profile, cfg Config) {
	now := h.now()
	out := Info{Name: u.Name, Brand: cfg.Brand, SupportURL: cfg.SupportURL, State: domain.State(u, now), UsedUp: u.UsedUp, UsedDown: u.UsedDown}
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
	for _, in := range prof.Inbounds {
		out.Protocols = append(out.Protocols, in.Preset)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
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
