package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"unicode/utf8"
)

// The admin pages of an addon of protocol 2 (docs/pipeline/addons.md, section 9): its
// /v2/info names an entry under /ui/, the panel proxies the files under /ui/ to a sandboxed
// frame and forwards the frame's calls to POST /v2/ui/rpc.

// Limits of what the panel takes from an addon's pages.
const (
	MaxPageFile  = 8 << 20 // one file of the pages
	MaxRPCAnswer = 1 << 20 // one answer to a call of the pages
)

// ErrBadAnswer: an addon's answer that is too large, not JSON, or a redirect.
var ErrBadAnswer = errors.New("addon_bad_answer")

// UI is where an addon's pages start and what the admin panel calls them.
type UI struct {
	Entry string `json:"entry"` // /ui/index.html
	Title Text   `json:"title"`
}

// InfoV2 is the description an addon of protocol 2 gives of itself at /v2/info. The signed
// catalog entry stays the truth for the permissions; this one is read for texts, settings
// and pages.
type InfoV2 struct {
	ID          string   `json:"id"`
	Protocol    int      `json:"protocol"`
	Version     string   `json:"version"`
	Kind        string   `json:"kind"`
	Name        Text     `json:"name"`
	Description Text     `json:"description"`
	Permissions []string `json:"permissions"`
	Settings    []Field  `json:"settings"`
	UI          *UI      `json:"ui,omitempty"`
	Help        Text     `json:"help"`
}

// validEntry: a plain file path under /ui/, nothing to escape or to climb out with.
var validEntry = regexp.MustCompile(`^/ui/[A-Za-z0-9._~/-]{0,200}$`)

func (u *UI) valid() bool {
	return u != nil && validEntry.MatchString(u.Entry) && (path.Clean(u.Entry) == u.Entry || u.Entry == "/ui/")
}

type cachedInfoV2 struct {
	build string
	info  InfoV2
}

// Served is a running addon of protocol 2 that serves HTTP: the only kind with pages.
func (m *Manager) Served(id string) (Installed, error) {
	s, err := m.State()
	if err != nil {
		return Installed{}, err
	}
	a, ok := s.Adapters[id]
	if !ok || a.Protocol != Protocol2 || a.Status != "running" || a.Mode == ModeClient || a.Listen == "" {
		return Installed{}, ErrNotInstalled
	}
	return a, nil
}

// InfoV2 is a running addon's /v2/info, asked once per installed build. Pages it names
// outside /ui/ are dropped: the panel serves nothing else of an addon.
func (m *Manager) InfoV2(ctx context.Context, id string) (InfoV2, error) {
	a, err := m.Served(id)
	if err != nil {
		return InfoV2{}, err
	}
	// An external addon has no digest: a new run of it is told by the host's time.
	build := a.Digest + "\x00" + a.Version + "\x00" + a.At
	m.mu.Lock()
	if c, ok := m.infos2[id]; ok && c.build == build {
		m.mu.Unlock()
		return c.info, nil
	}
	m.mu.Unlock()
	var info InfoV2
	if err := NewClient("http://"+a.Listen, a.Token, m.pages).call(ctx, http.MethodGet, "/v2/info", nil, &info); err != nil {
		return InfoV2{}, err
	}
	if info.ID != id || info.Protocol != Protocol2 {
		return InfoV2{}, fmt.Errorf("%w: %s answers as %q, protocol %d", ErrBadAnswer, id, info.ID, info.Protocol)
	}
	if !info.UI.valid() {
		info.UI = nil
	}
	m.mu.Lock()
	m.infos2[id] = cachedInfoV2{build: build, info: info}
	m.mu.Unlock()
	return info, nil
}

// PageFile is one file of an addon's pages as the panel passes it on.
type PageFile struct {
	Status int
	Header http.Header // only the headers safe to pass on
	Body   []byte
}

// pageRequestHeaders are the browser's headers an addon gets with a file: none that carry
// the admin's credentials.
var pageRequestHeaders = []string{"Accept", "Accept-Language", "If-None-Match", "If-Modified-Since"}

// pageResponseHeaders are the addon's headers the browser gets: nothing that sets cookies,
// redirects or changes the panel's own security headers.
var pageResponseHeaders = []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified"}

// Page fetches GET /ui/<name> of a running addon with its token. hdr is the browser's
// request: only pageRequestHeaders of it go on.
func (m *Manager) Page(ctx context.Context, id, name, rawQuery string, hdr http.Header) (PageFile, error) {
	a, err := m.Served(id)
	if err != nil {
		return PageFile{}, err
	}
	u := url.URL{Scheme: "http", Host: a.Listen, Path: "/ui/" + name, RawQuery: rawQuery}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return PageFile{}, err
	}
	for _, k := range pageRequestHeaders {
		if v := hdr.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	resp, err := m.pages.Do(req)
	if err != nil {
		return PageFile{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 3 && resp.StatusCode != http.StatusNotModified {
		return PageFile{}, ErrBadAnswer
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxPageFile+1))
	if err != nil {
		return PageFile{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if len(body) > MaxPageFile {
		return PageFile{}, ErrBadAnswer
	}
	f := PageFile{Status: resp.StatusCode, Header: http.Header{}, Body: body}
	for _, k := range pageResponseHeaders {
		if v := resp.Header.Get(k); v != "" {
			f.Header.Set(k, v)
		}
	}
	return f, nil
}

// RPC is a call of an addon's pages, as the panel forwards it to POST /v2/ui/rpc.
type RPC struct {
	Method string          `json:"method"` // GET | POST | PUT | DELETE
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
	Admin  RPCAdmin        `json:"admin"`
}

// RPCAdmin is what the addon learns of the admin who called.
type RPCAdmin struct {
	Lang string `json:"lang"`
}

// CallUI forwards a call of the pages to the addon and returns its status and its JSON as
// they are, errors included: the pages read them. Only a broken answer is an error here.
func (m *Manager) CallUI(ctx context.Context, id string, call RPC) (int, json.RawMessage, error) {
	a, err := m.Served(id)
	if err != nil {
		return 0, nil, err
	}
	data, err := json.Marshal(call)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+a.Listen+"/v2/ui/rpc", bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.pages.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxRPCAnswer+1))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if len(body) > MaxRPCAnswer || resp.StatusCode/100 == 3 || resp.StatusCode < 200 {
		return 0, nil, ErrBadAnswer
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return resp.StatusCode, json.RawMessage("null"), nil
	}
	if !utf8.Valid(body) || !json.Valid(body) {
		return 0, nil, ErrBadAnswer
	}
	return resp.StatusCode, json.RawMessage(body), nil
}
