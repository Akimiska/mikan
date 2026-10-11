package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/store/db"
)

// The marketplace of addons of protocol 2: bots, pages and the rest (the payment adapters
// have addons.go). Installing one is approving what it may do through the panel's API for
// addons: the admin sees the permissions and approves exactly them, and an update asking
// for more waits for a new approval. Only a session may do it.

type MarketEntry struct {
	ID          string            `json:"id"`
	Name        map[string]string `json:"name" doc:"Название по языкам"`
	Description map[string]string `json:"description"`
	Version     string            `json:"version"`
	Kind        string            `json:"kind" enum:"bot,ui,generic"`
	Mode        string            `json:"mode" enum:"http,client"`
	Permissions []string          `json:"permissions" doc:"Что аддон просит: это и одобряет админ при установке"`
	Homepage    string            `json:"homepage"`
	Installed   bool              `json:"installed"`
	Update      bool              `json:"update" doc:"Установлен, а в каталоге другая сборка"`
	Wider       bool              `json:"wider" doc:"Сборка каталога просит больше прав, чем одобрено: обновление ждёт нового одобрения"`
}

type MarketInstalled struct {
	ID      string   `json:"id"`
	Version string   `json:"version"`
	Kind    string   `json:"kind" enum:"bot,ui,generic"`
	Status  string   `json:"status" enum:"running,failed"`
	Error   string   `json:"error,omitempty" doc:"Почему контейнер не работает"`
	Granted []string `json:"granted" doc:"Одобренные права"`
}

type MarketView struct {
	Catalog      []MarketEntry     `json:"catalog"`
	CatalogError string            `json:"catalog_error,omitempty" doc:"catalog_unavailable — каталог не загрузился"`
	Installed    []MarketInstalled `json:"installed"`
	Pending      *AddonRequest     `json:"pending,omitempty" doc:"Заявка, которую сервер ещё не взял"`
	Last         *AddonResult      `json:"last,omitempty" doc:"Как сервер выполнил последнюю заявку"`
	Supported    bool              `json:"supported" doc:"Панель видит каталог данных сервера; иначе ставить аддоны нельзя"`
	HostReady    bool              `json:"host_ready" doc:"mikan на сервере умеет аддоны протокола 2; иначе сначала обновить сервер"`
}

type marketOutput struct{ Body MarketView }

type marketInstallInput struct {
	ID   string `path:"id" pattern:"^[a-z0-9][a-z0-9-]{0,31}$"`
	Body struct {
		Permissions []string `json:"permissions" doc:"Права, которые админ видел и одобрил: ровно те, что просит сборка каталога"`
	}
}

func (h *handlers) registerMarketplace() {
	tags := []string{"addons"}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: tags, DefaultStatus: status, Metadata: sessionOnly, Extensions: sessionOnlyExt}
	}
	huma.Register(h.api, op("list-marketplace", http.MethodGet, "/api/v1/marketplace", "Маркетплейс аддонов: боты, страницы и прочее", 0), h.listMarketplace)
	huma.Register(h.api, op("install-marketplace", http.MethodPost, "/api/v1/marketplace/{id}/install", "Одобрить права и установить или обновить аддон: заявка серверу", http.StatusAccepted), h.installMarketplace)
	huma.Register(h.api, op("remove-marketplace", http.MethodPost, "/api/v1/marketplace/{id}/remove", "Удалить аддон и отозвать его права: заявка серверу", http.StatusAccepted), h.removeMarketplace)
}

// granted is the addon's approved permissions; nil when it has no grant.
func (h *handlers) granted(ctx context.Context, id string) (*db.AddonGrant, []string, error) {
	g, err := h.d.Store.Q.GetAddonGrant(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var perms []string
	if err := json.Unmarshal([]byte(g.Permissions), &perms); err != nil {
		return nil, nil, err
	}
	return &g, perms, nil
}

func (h *handlers) marketView(ctx context.Context) (MarketView, error) {
	v := MarketView{Catalog: []MarketEntry{}, Installed: []MarketInstalled{}}
	m := h.d.Addons
	if m == nil {
		return v, nil
	}
	st, err := m.State()
	if err != nil {
		return v, err
	}
	v.Supported, v.HostReady = m.Supported(), st.HostVersion != ""
	if l := st.Request; l != nil {
		v.Last = &AddonResult{Action: l.Action, ID: l.ID, State: l.State, Error: l.Error, At: l.At}
	}
	if q := m.Pending(); len(q) > 0 {
		v.Pending = &AddonRequest{Action: q[0].Action, ID: q[0].ID, At: q[0].At}
	}
	installed := map[string]addons.Installed{}
	for id, a := range st.Adapters {
		if a.Protocol == addons.Protocol2 {
			installed[id] = a
		}
	}
	grants := map[string][]string{}
	for _, id := range slices.Sorted(maps.Keys(installed)) {
		a := installed[id]
		_, perms, err := h.granted(ctx, id)
		if err != nil {
			return v, err
		}
		grants[id] = perms
		v.Installed = append(v.Installed, MarketInstalled{ID: id, Version: a.Version, Kind: a.KindOf(), Status: a.Status, Error: a.Error, Granted: listOrEmpty(perms)})
	}
	if c, err := h.catalog(ctx); err != nil {
		v.CatalogError = "catalog_unavailable"
	} else {
		for _, e := range c.Adapters {
			if e.Protocol != addons.Protocol2 {
				continue
			}
			a, ok := installed[e.ID]
			v.Catalog = append(v.Catalog, MarketEntry{ID: e.ID, Name: e.Name, Description: e.Description, Version: e.Version, Kind: e.Kind, Mode: e.Mode,
				Permissions: listOrEmpty(e.Permissions), Homepage: e.Homepage, Installed: ok, Update: ok && a.Digest != e.Digest,
				Wider: ok && !addons.Covers(grants[e.ID], e.Permissions)})
		}
	}
	return v, nil
}

func listOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (h *handlers) listMarketplace(ctx context.Context, _ *struct{}) (*marketOutput, error) {
	v, err := h.marketView(ctx)
	if err != nil {
		return nil, err
	}
	return &marketOutput{Body: v}, nil
}

// installMarketplace records the approval first and then asks the host: an addon never runs
// with permissions the admin has not seen.
func (h *handlers) installMarketplace(ctx context.Context, in *marketInstallInput) (*marketOutput, error) {
	if h.d.Addons == nil {
		return nil, huma.Error409Conflict("addons_unavailable")
	}
	st, err := h.d.Addons.State()
	if err != nil {
		return nil, err
	}
	if st.HostVersion == "" {
		return nil, huma.Error409Conflict("host_outdated")
	}
	c, err := h.catalog(ctx)
	if err != nil {
		return nil, huma.Error502BadGateway("catalog_unavailable")
	}
	i := slices.IndexFunc(c.Adapters, func(e addons.Entry) bool { return e.ID == in.ID && e.Protocol == addons.Protocol2 })
	if i < 0 {
		return nil, huma.Error404NotFound("addon_unknown")
	}
	e := c.Adapters[i]
	if a, ok := st.Adapters[in.ID]; ok && a.Status == "running" && a.Digest == e.Digest {
		return nil, huma.Error409Conflict("addon_current")
	}
	// The admin approves what they were shown; a catalog that changed in between is shown
	// again rather than approved blindly.
	if !addons.SamePermissions(in.Body.Permissions, e.Permissions) {
		return nil, huma.Error409Conflict("permissions_changed")
	}
	if queued(h.d.Addons, e.ID) {
		return nil, huma.Error409Conflict("addon_busy")
	}
	admin := sessionOf(ctx).AdminID
	if err := h.d.Store.Q.UpsertAddonGrant(ctx, db.UpsertAddonGrantParams{AddonID: e.ID, Version: e.Version,
		Permissions: addons.EncodePermissions(e.Permissions), ApprovedBy: sql.NullInt64{Int64: admin, Valid: true}, ApprovedAt: h.d.Now().Unix()}); err != nil {
		return nil, err
	}
	if err := h.d.Addons.Ask(addons.Request{Action: "install", ID: e.ID}); err != nil {
		return nil, askErr(err)
	}
	h.audit(ctx, admin, "addon.install", "addon", e.ID, map[string]any{"version": e.Version, "digest": e.Digest, "permissions": e.Permissions})
	return h.listMarketplace(ctx, nil)
}

// removeMarketplace takes the approval back at once: the addon loses the panel's API before
// the host has even stopped it.
func (h *handlers) removeMarketplace(ctx context.Context, in *addonIDInput) (*marketOutput, error) {
	if h.d.Addons == nil {
		return nil, huma.Error409Conflict("addons_unavailable")
	}
	st, err := h.d.Addons.State()
	if err != nil {
		return nil, err
	}
	if a, ok := st.Adapters[in.ID]; !ok || a.Protocol != addons.Protocol2 {
		return nil, huma.Error404NotFound("addon_not_installed")
	}
	if queued(h.d.Addons, in.ID) {
		return nil, huma.Error409Conflict("addon_busy")
	}
	if err := h.d.Store.Q.DeleteAddonGrant(ctx, in.ID); err != nil {
		return nil, err
	}
	if err := h.d.Addons.Ask(addons.Request{Action: "remove", ID: in.ID}); err != nil {
		return nil, askErr(err)
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "addon.remove", "addon", in.ID, nil)
	return h.listMarketplace(ctx, nil)
}

// queued: a request for the addon still waits for the host; the grant is not touched under
// it, so the approval and the request the host takes never disagree.
func queued(m *addons.Manager, id string) bool {
	return slices.ContainsFunc(m.Pending(), func(r addons.Request) bool { return r.ID == id })
}
