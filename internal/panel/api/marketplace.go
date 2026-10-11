package api

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/store/db"
	"mikan/internal/release"
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
	Version string   `json:"version" doc:"Пусто у аддона админа: версию знает только каталог"`
	Kind    string   `json:"kind" enum:"bot,ui,generic"`
	Mode    string   `json:"mode" enum:"http,client"`
	Source  string   `json:"source" enum:"official,custom,external" doc:"official — из подписанного каталога; custom и external — аддон админа, mikan его не проверял"`
	Image   string   `json:"image,omitempty" doc:"Образ аддона админа (custom)"`
	Digest  string   `json:"digest,omitempty" doc:"Дайджест образа аддона админа (custom)"`
	URL     string   `json:"url,omitempty" doc:"Адрес внешнего аддона (external); пусто у клиента панели"`
	MemMB   int      `json:"mem_mb,omitempty" doc:"Память контейнера, МБ; 0 — по умолчанию 128"`
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

type marketCustomInput struct {
	Body struct {
		ID          string   `json:"id" maxLength:"32" doc:"Имя аддона: a-z, 0-9 и дефис, не из каталога"`
		Source      string   `json:"source" enum:"custom,external" doc:"custom — свой образ по дайджесту; external — свой процесс на 127.0.0.1"`
		Image       string   `json:"image,omitempty" maxLength:"255" doc:"custom: образ без тега, например ghcr.io/me/bot"`
		Digest      string   `json:"digest,omitempty" maxLength:"71" doc:"custom: sha256:<64 hex>"`
		URL         string   `json:"url,omitempty" maxLength:"64" doc:"external: http://127.0.0.1:<порт>; клиенту панели (mode client) можно не указывать"`
		Kind        string   `json:"kind" enum:"bot,ui,generic"`
		Mode        string   `json:"mode" enum:"http,client" doc:"http — аддон отвечает панели на /v2/*; client — только сам ходит в панель"`
		Permissions []string `json:"permissions" maxItems:"16" doc:"Права, которые админ даёт аддону"`
		MemMB       int      `json:"mem_mb,omitempty" doc:"Память контейнера, 64–256 МБ; 0 — по умолчанию 128"`
		Confirm     bool     `json:"confirm" doc:"Админ подтвердил, что доверяет этому коду: mikan его не проверял"`
	}
}

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
	huma.Register(h.api, op("install-custom-addon", http.MethodPost, "/api/v1/marketplace/custom", "Установить свой аддон: образ по дайджесту или процесс на 127.0.0.1, без проверки mikan", http.StatusAccepted), h.installCustom)
	huma.Register(h.api, op("remove-marketplace", http.MethodPost, "/api/v1/marketplace/{id}/remove", "Удалить аддон и отозвать его права: заявка серверу", http.StatusAccepted), h.removeMarketplace)
}

// granted is the addon's approved permissions; nil when it has no grant.
func (h *handlers) granted(ctx context.Context, id string) ([]string, error) {
	g, err := h.d.Store.Q.GetAddonGrant(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return addons.DecodePermissions(g.Permissions)
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
		perms, err := h.granted(ctx, id)
		if err != nil {
			return v, err
		}
		grants[id] = perms
		mi := MarketInstalled{ID: id, Version: a.Version, Kind: a.KindOf(), Mode: a.Mode, Source: a.SourceOf(), MemMB: a.MemMB, Status: a.Status, Error: a.Error, Granted: listOrEmpty(perms)}
		if mi.Mode == "" {
			mi.Mode = addons.ModeHTTP
		}
		// Where an addon of the admin's own comes from is all there is to tell it by: nothing
		// signed names it.
		switch mi.Source {
		case addons.SourceCustom:
			mi.Image, mi.Digest = a.Image, a.Digest
		case addons.SourceExternal:
			if a.Listen != "" {
				mi.URL = "http://" + a.Listen
			}
		}
		v.Installed = append(v.Installed, mi)
	}
	if c, err := h.catalog(ctx); err != nil {
		v.CatalogError = "catalog_unavailable"
	} else {
		for _, e := range c.Adapters {
			if e.Protocol != addons.Protocol2 {
				continue
			}
			a, ok := installed[e.ID]
			// An addon of the admin's own under an id the catalog took later is not this one.
			ok = ok && a.SourceOf() == addons.SourceOfficial
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
	a, ok := st.Adapters[in.ID]
	if ok && a.SourceOf() != addons.SourceOfficial {
		// The admin's own addon holds the id: the catalog's would take over its tokens and grant.
		return nil, huma.Error409Conflict("addon_id_taken")
	}
	if ok && a.Status == "running" && a.Digest == e.Digest {
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

// customGrantVersion is the grant's version of an addon of the admin's own: no catalog build
// stands behind it.
const customGrantVersion = "custom"

// installCustom runs code nothing signed vouches for: an image the admin names by its digest,
// or a process of theirs on a loopback port. Only a session may, and only with the admin's
// word that they trust the code. Its id may not be one the catalog has, so it never passes
// for an official addon (the switch to the bot addon looks for "telegram").
func (h *handlers) installCustom(ctx context.Context, in *marketCustomInput) (*marketOutput, error) {
	// The operation is session only; this is the same refusal again, should its mark be lost.
	if err := requireSession(ctx, "source"); err != nil {
		return nil, err
	}
	b := in.Body
	r := addons.Request{Action: "install", ID: b.ID, Source: b.Source, Kind: b.Kind, Mode: b.Mode, Permissions: b.Permissions, MemMB: b.MemMB}
	var bad []error
	if !addons.ValidID(b.ID) {
		bad = append(bad, fieldDetail("body.id", "addon_id_invalid", nil))
	}
	switch b.Source {
	case addons.SourceCustom:
		r.Image, r.Digest = b.Image, b.Digest
		if !addons.ValidImage(b.Image) {
			bad = append(bad, fieldDetail("body.image", "addon_image_invalid", nil))
		}
		if !release.ValidDigest(b.Digest) {
			bad = append(bad, fieldDetail("body.digest", "addon_digest_invalid", nil))
		}
	case addons.SourceExternal:
		r.URL = b.URL
		if !addons.ValidURL(b.URL) && (b.URL != "" || b.Mode != addons.ModeClient) {
			bad = append(bad, fieldDetail("body.url", "addon_url_invalid", nil))
		}
	}
	seen := map[string]bool{}
	for _, p := range b.Permissions {
		if seen[p] || !slices.Contains(addons.Permissions, p) {
			bad = append(bad, fieldDetail("body.permissions", "addon_permissions_invalid", p))
			break
		}
		seen[p] = true
	}
	if b.MemMB != 0 && (b.MemMB < addons.MinMemMB || b.MemMB > addons.MaxMemMB) {
		bad = append(bad, fieldDetail("body.mem_mb", "addon_mem_invalid", nil))
	}
	if !b.Confirm {
		bad = append(bad, fieldDetail("body.confirm", "addon_not_confirmed", nil))
	}
	if len(bad) > 0 {
		return nil, fieldErrs(bad...)
	}
	if !r.Valid() {
		// What the fields above let through and the host would still refuse.
		return nil, huma.Error422UnprocessableEntity("addon_request_invalid")
	}
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
	// Without the catalog the id cannot be checked against it: refused rather than guessed.
	c, err := h.catalog(ctx)
	if err != nil {
		return nil, huma.Error502BadGateway("catalog_unavailable")
	}
	if slices.ContainsFunc(c.Adapters, func(e addons.Entry) bool { return e.ID == b.ID }) {
		return nil, fieldErr("body.id", "addon_id_taken")
	}
	// Another program under this id (the catalog's, a payment adapter, the other source) goes
	// first: an install over it would take over its tokens.
	if a, ok := st.Adapters[b.ID]; ok && a.SourceOf() != b.Source {
		return nil, fieldErr("body.id", "addon_id_taken")
	}
	if queued(h.d.Addons, b.ID) {
		return nil, huma.Error409Conflict("addon_busy")
	}
	admin := sessionOf(ctx).AdminID
	if err := h.d.Store.Q.UpsertAddonGrant(ctx, db.UpsertAddonGrantParams{AddonID: b.ID, Version: customGrantVersion,
		Permissions: addons.EncodePermissions(b.Permissions), ApprovedBy: sql.NullInt64{Int64: admin, Valid: true}, ApprovedAt: h.d.Now().Unix()}); err != nil {
		return nil, err
	}
	if err := h.d.Addons.Ask(r); err != nil {
		return nil, askErr(err)
	}
	// Where the code comes from and what it may do; nothing here is a secret.
	h.audit(ctx, admin, "addon.install", "addon", b.ID, map[string]any{"source": b.Source, "image": r.Image, "digest": r.Digest, "url": r.URL,
		"kind": b.Kind, "mode": b.Mode, "permissions": listOrEmpty(b.Permissions), "mem_mb": b.MemMB})
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
	// The grant goes first, and the addon's store and its place in the feed with it: another
	// addon installed later under the same id starts with nothing of this one's.
	if err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteAddonGrant(ctx, in.ID); err != nil {
			return err
		}
		if err := q.DeleteAddonData(ctx, in.ID); err != nil {
			return err
		}
		return q.DeleteAddonCursor(ctx, in.ID)
	}); err != nil {
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
