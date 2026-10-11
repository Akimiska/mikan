// Package panelapi is the panel's API for addons of protocol 2 (docs: PROTOCOL-v2.md in the
// marketplace repository). It has a listener of its own on the loopback address the host
// gave out in state.json, never the public port, and it knows no admin session: an addon
// proves itself with the panel token the host gave it, and may call only what its grant,
// approved by the admin, holds.
package panelapi

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/events"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Prefix is where the API lives on its listener; the host hands it to addons as
// MIKAN_PANEL_URL.
const Prefix = "/panel/v2"

// MaxBody bounds what an addon may send in one request.
const MaxBody = 1 << 20

// Grants is what the server reads the admin's approvals from.
type Grants interface {
	GetAddonGrant(ctx context.Context, addonID string) (db.AddonGrant, error)
}

type Deps struct {
	Addons *addons.Manager
	Grants Grants
	Store  *store.Store // nil: no kv and no events (tests of access alone)
	Hub    *events.Hub
	Log    *slog.Logger
	Now    func() time.Time
}

// Caller is the addon a request came from and what it may do.
type Caller struct {
	ID          string
	Version     string
	Permissions []string
}

func (c Caller) Can(perm string) bool { return slices.Contains(c.Permissions, perm) }

type callerKey struct{}

// CallerOf is the addon behind a request that passed the server.
func CallerOf(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// Error is the body of every refusal: the same shape as the adapters' own errors.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Fail answers with an error; message must not carry secrets.
func Fail(w http.ResponseWriter, status int, code, message string) {
	Write(w, status, Error{Code: code, Message: message})
}

// Write answers with JSON.
func Write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Server serves the API; Run keeps its listener on the address the host gave out.
type Server struct {
	d Deps

	mux *http.ServeMux // the routes, each behind its permission

	mu   sync.Mutex
	addr string // what it listens on, "" while it does not
}

func New(d Deps) *Server {
	s := &Server{d: d, mux: http.NewServeMux()}
	s.mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		Fail(w, http.StatusNotFound, "not_found", "no such method")
	})
	s.Handle(http.MethodGet, "/self", "", s.self)
	if d.Store != nil {
		s.handleKV()
		s.handleEvents()
	}
	return s
}

// Handle adds a route below Prefix that needs perm; "" lets any approved addon in. path is a
// ServeMux pattern ("/kv/{key}"). Routes are added before Run.
func (s *Server) Handle(method, path, perm string, h http.HandlerFunc) {
	if perm != "" && !slices.Contains(addons.Permissions, perm) {
		panic("panelapi: unknown permission " + perm)
	}
	s.mux.HandleFunc(method+" "+Prefix+path, func(w http.ResponseWriter, r *http.Request) {
		if c, _ := CallerOf(r.Context()); perm != "" && !c.Can(perm) {
			Fail(w, http.StatusForbidden, "forbidden", "the addon has no permission "+perm)
			return
		}
		h(w, r)
	})
}

// ServeHTTP lets in an addon by its token, then a route its grant allows.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A browser has no business here: an Origin is a page trying the loopback port.
	if r.Header.Get("Origin") != "" {
		Fail(w, http.StatusForbidden, "forbidden", "not for browsers")
		return
	}
	// The Host must be the address listened on, so a name that resolves to loopback does
	// not let a page in (DNS rebinding).
	s.mu.Lock()
	addr := s.addr
	s.mu.Unlock()
	if addr != "" && r.Host != addr {
		Fail(w, http.StatusMisdirectedRequest, "bad_request", "unknown host")
		return
	}
	c, status, code := s.authenticate(r)
	if status != 0 {
		Fail(w, status, code, code)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, c)))
}

// authenticate finds the running addon of protocol 2 the token belongs to, and its grant;
// it answers a status and a code when there is none.
func (s *Server) authenticate(r *http.Request) (Caller, int, string) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return Caller{}, http.StatusUnauthorized, "unauthorized"
	}
	st, err := s.d.Addons.State()
	if err != nil {
		s.d.Log.Warn("addons api: state", "err", err)
		return Caller{}, http.StatusServiceUnavailable, "unavailable"
	}
	id := ""
	// Every token is compared, so the time does not tell how far one matched.
	for aid, a := range st.Adapters {
		if a.Protocol == addons.Protocol2 && a.PanelToken != "" &&
			subtle.ConstantTimeCompare([]byte(a.PanelToken), []byte(token)) == 1 && a.Status == "running" {
			id = aid
		}
	}
	if id == "" {
		return Caller{}, http.StatusUnauthorized, "unauthorized"
	}
	g, err := s.d.Grants.GetAddonGrant(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return Caller{}, http.StatusForbidden, "not_approved"
	}
	if err != nil {
		s.d.Log.Warn("addons api: grant", "addon", id, "err", err)
		return Caller{}, http.StatusServiceUnavailable, "unavailable"
	}
	var perms []string
	if err := json.Unmarshal([]byte(g.Permissions), &perms); err != nil {
		s.d.Log.Warn("addons api: grant", "addon", id, "err", err)
		return Caller{}, http.StatusServiceUnavailable, "unavailable"
	}
	return Caller{ID: id, Version: g.Version, Permissions: perms}, 0, ""
}

// self tells an addon who the panel takes it for and what it may do.
func (s *Server) self(w http.ResponseWriter, r *http.Request) {
	c, _ := CallerOf(r.Context())
	Write(w, http.StatusOK, map[string]any{"id": c.ID, "version": c.Version, "permissions": c.Permissions})
}

// Loopback: an address on 127.0.0.1 with an unprivileged port, as the host gives out.
func Loopback(addr string) bool {
	ap, err := netip.ParseAddrPort(addr)
	return err == nil && ap.Addr() == netip.MustParseAddr("127.0.0.1") && ap.Port() >= 1024
}

// Run keeps the listener on the address of the host's state: none while no addon of
// protocol 2 was installed, a new one when the address changes (a restored backup).
func (s *Server) Run(ctx context.Context) {
	var srv *http.Server
	// Run returns only after the requests in flight are done: the database closes after it.
	var wg sync.WaitGroup
	defer wg.Wait()
	stop := func() {
		if srv != nil {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(sctx)
			cancel()
			srv = nil
		}
		s.mu.Lock()
		s.addr = ""
		s.mu.Unlock()
	}
	defer stop()
	failed := "" // the address that would not bind: told once, tried again quietly
	check := func() {
		st, err := s.d.Addons.State()
		if err != nil {
			return
		}
		want := st.PanelAPI
		if !Loopback(want) {
			want = ""
		}
		s.mu.Lock()
		cur := s.addr
		s.mu.Unlock()
		if want == cur && (want == "") == (srv == nil) {
			return
		}
		stop()
		if want == "" {
			return
		}
		ln, err := net.Listen("tcp", want)
		if err != nil {
			if failed != want {
				s.d.Log.Warn("addons api: listen", "addr", want, "err", err)
			}
			failed = want
			return
		}
		failed = ""
		srv = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
			WriteTimeout: 90 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10}
		s.mu.Lock()
		s.addr = want
		s.mu.Unlock()
		wg.Add(1)
		go func(srv *http.Server) {
			defer wg.Done()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.d.Log.Warn("addons api: serve", "err", err)
			}
		}(srv)
		s.d.Log.Info("addons api: listening", "addr", want)
	}
	check()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	prune := time.NewTicker(10 * time.Minute)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		case <-prune.C:
			if s.d.Store != nil {
				s.pruneKV(ctx)
				if _, err := events.Prune(ctx, s.d.Store.Q, s.d.Now()); err != nil && ctx.Err() == nil {
					s.d.Log.Warn("addons api: prune events", "err", err)
				}
			}
		}
	}
}
