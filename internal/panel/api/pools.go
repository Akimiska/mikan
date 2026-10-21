package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Server groups (the traffic pools of GitHub issue #6): a set of inbounds that a tariff and
// a subscription hold in one mode each (domain.PoolMain and the rest). The inbounds in no
// group are the main servers.

type PoolView struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Inbounds   []string `json:"inbounds" doc:"Имена подключений группы"`
	InboundIDs []int64  `json:"inbound_ids" doc:"Подключения группы"`
}

type poolsOutput struct{ Body []PoolView }
type poolOutput struct{ Body PoolView }

type poolInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"40"`
	}
}

type poolPatchInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"40"`
	}
}

type groupInboundsInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		InboundIDs []int64 `json:"inbound_ids" maxItems:"1000" doc:"Все подключения группы: перечисленные переходят в неё из других групп, остальные её подключения возвращаются к основным серверам"`
	}
}

// GroupInTariff is how one tariff has the server group.
type GroupInTariff struct {
	TariffID     int64  `json:"tariff_id" minimum:"1"`
	Mode         string `json:"mode" enum:"main,own,unlimited,closed"`
	TrafficLimit *int64 `json:"traffic_limit,omitempty" minimum:"1" doc:"Байты за период; только с mode=own"`
}

type groupTariffsInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Tariffs []GroupInTariff `json:"tariffs" maxItems:"200" doc:"Тарифы, в которых группа меняется; остальные тарифы держат её как раньше. Закрытие и открытие группы действует сразу на клиентов тарифа"`
	}
}

// PoolSetting is how a tariff or a subscription has a server group.
type PoolSetting struct {
	PoolID       int64  `json:"pool_id" minimum:"1"`
	Mode         string `json:"mode" enum:"main,own,unlimited,closed" doc:"main — входит, трафик считается в основной; own — входит со своим лимитом traffic_limit; unlimited — входит без лимита, отдельно от основного; closed — не входит: подключений группы нет в подписке, ноды их не пускают, пакеты группы не продаются"`
	TrafficLimit *int64 `json:"traffic_limit,omitempty" minimum:"1" doc:"Байты за период; только с mode=own"`
}

// UserPoolView is a server group of a user: how the subscription has it and what it used
// this period.
type UserPoolView struct {
	PoolID       int64  `json:"pool_id"`
	Name         string `json:"name"`
	Mode         string `json:"mode" enum:"main,own,unlimited,closed"`
	TrafficLimit *int64 `json:"traffic_limit" doc:"Байты за период с mode=own; иначе null"`
	UsedUp       int64  `json:"used_up"`
	UsedDown     int64  `json:"used_down"`
	Extra        int64  `json:"extra" doc:"Байты, оставшиеся в пакетах трафика группы: тратятся после лимита"`
	Exhausted    bool   `json:"exhausted" doc:"Свой лимит группы и её пакеты исчерпаны: подключения группы не работают до сброса"`
}

type userPoolsOutput struct{ Body []UserPoolView }

type userPoolsInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Pools []PoolSetting `json:"pools" maxItems:"100" doc:"Группы, которые меняются; остальные остаются как есть"`
	}
}

func (h *handlers) registerPools() {
	tags := []string{"inbounds"}
	huma.Register(h.api, huma.Operation{OperationID: "list-pools", Method: http.MethodGet, Path: "/api/v1/pools", Summary: "Группы серверов", Tags: tags}, h.listPools)
	huma.Register(h.api, huma.Operation{OperationID: "create-pool", Method: http.MethodPost, Path: "/api/v1/pools", Summary: "Создать группу серверов", Tags: tags, DefaultStatus: http.StatusCreated}, h.createPool)
	huma.Register(h.api, huma.Operation{OperationID: "rename-pool", Method: http.MethodPatch, Path: "/api/v1/pools/{id}", Summary: "Переименовать группу серверов", Tags: tags}, h.renamePool)
	huma.Register(h.api, huma.Operation{OperationID: "set-pool-inbounds", Method: http.MethodPut, Path: "/api/v1/pools/{id}/inbounds", Summary: "Подключения группы серверов", Tags: tags}, h.setPoolInbounds)
	huma.Register(h.api, huma.Operation{OperationID: "set-pool-tariffs", Method: http.MethodPut, Path: "/api/v1/pools/{id}/tariffs", Summary: "Как тарифы держат группу серверов", Tags: tags}, h.setPoolTariffs)
	huma.Register(h.api, huma.Operation{OperationID: "delete-pool", Method: http.MethodDelete, Path: "/api/v1/pools/{id}", Summary: "Удалить группу серверов: её подключения вернутся к основным серверам; пока в ней есть оплаченный трафик, группа остаётся", Tags: tags, DefaultStatus: http.StatusNoContent}, h.deletePool)
	huma.Register(h.api, huma.Operation{OperationID: "user-pools", Method: http.MethodGet, Path: "/api/v1/users/{id}/pools", Summary: "Группы серверов пользователя", Tags: []string{"users"}}, h.userPools)
	huma.Register(h.api, huma.Operation{OperationID: "set-user-pools", Method: http.MethodPut, Path: "/api/v1/users/{id}/pools", Summary: "Как подписка пользователя держит группы серверов", Tags: []string{"users"}}, h.setUserPools)
}

func (h *handlers) pools(ctx context.Context) ([]PoolView, error) {
	ps, err := h.d.Store.Q.ListTrafficPools(ctx)
	if err != nil {
		return nil, err
	}
	ins, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PoolView, 0, len(ps))
	for _, p := range ps {
		v := PoolView{ID: p.ID, Name: p.Name, Inbounds: []string{}, InboundIDs: []int64{}}
		for _, in := range ins {
			if in.PoolID.Valid && in.PoolID.Int64 == p.ID {
				v.Inbounds = append(v.Inbounds, in.Name)
				v.InboundIDs = append(v.InboundIDs, in.ID)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (h *handlers) pool(ctx context.Context, id int64) (*poolOutput, error) {
	ps, err := h.pools(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.ID == id {
			return &poolOutput{Body: p}, nil
		}
	}
	return nil, huma.Error404NotFound("not_found")
}

func (h *handlers) listPools(ctx context.Context, _ *struct{}) (*poolsOutput, error) {
	ps, err := h.pools(ctx)
	if err != nil {
		return nil, err
	}
	return &poolsOutput{Body: ps}, nil
}

func poolNameError(err error) error {
	if store.IsUnique(err) {
		return huma.Error409Conflict("pool_name_taken", &huma.ErrorDetail{Location: "body.name", Message: "pool_name_taken"})
	}
	return err
}

func (h *handlers) createPool(ctx context.Context, in *poolInput) (*poolOutput, error) {
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, fieldErr("body.name", "name_blank")
	}
	p, err := h.d.Store.Q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: name, CreatedAt: h.d.Now().Unix()})
	if err != nil {
		return nil, poolNameError(err)
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "pool.create", "pool", strconv.FormatInt(p.ID, 10), map[string]any{"name": name})
	return &poolOutput{Body: PoolView{ID: p.ID, Name: p.Name, Inbounds: []string{}, InboundIDs: []int64{}}}, nil
}

func (h *handlers) renamePool(ctx context.Context, in *poolPatchInput) (*poolOutput, error) {
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, fieldErr("body.name", "name_blank")
	}
	n, err := h.d.Store.Q.RenameTrafficPool(ctx, db.RenameTrafficPoolParams{Name: name, ID: in.ID})
	if err != nil {
		return nil, poolNameError(err)
	}
	if n == 0 {
		return nil, huma.Error404NotFound("not_found")
	}
	return h.pool(ctx, in.ID)
}

// setPoolInbounds makes the listed inbounds the group's, all at once: they leave the
// groups they were in, and the group's inbounds the list leaves out go back to the main
// servers. The nodes count the inbounds to their new quotas from now.
func (h *handlers) setPoolInbounds(ctx context.Context, in *groupInboundsInput) (*poolOutput, error) {
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		for _, id := range in.Body.InboundIDs {
			if _, err := q.GetInbound(ctx, id); errors.Is(err, sql.ErrNoRows) {
				return fieldErrValue("body.inbound_ids", "inbound_not_found", id)
			} else if err != nil {
				return err
			}
		}
		return domain.SetGroupInbounds(ctx, q, in.ID, in.Body.InboundIDs)
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	h.d.Changes.SlotsChanged()
	h.d.Changes.PoliciesChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "pool.inbounds", "pool", strconv.FormatInt(in.ID, 10), map[string]any{"inbounds": in.Body.InboundIDs})
	return h.pool(ctx, in.ID)
}

// setPoolTariffs sets how each listed tariff has the group, all in one transaction: the
// tariff's other groups stay as they are, and its gifts are checked again, since a group
// out of its own limit leaves a gift for it adding to no quota.
func (h *handlers) setPoolTariffs(ctx context.Context, in *groupTariffsInput) (*poolOutput, error) {
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		if _, err := q.GetTrafficPool(ctx, in.ID); errors.Is(err, sql.ErrNoRows) {
			return huma.Error404NotFound("not_found")
		} else if err != nil {
			return err
		}
		seen := map[int64]bool{}
		for _, e := range in.Body.Tariffs {
			if seen[e.TariffID] {
				return fieldErrValue("body.tariffs", "tariff_duplicate", e.TariffID)
			}
			seen[e.TariffID] = true
			t, err := q.GetTariff(ctx, e.TariffID)
			if errors.Is(err, sql.ErrNoRows) {
				return fieldErrValue("body.tariffs", "tariff_not_found", e.TariffID)
			} else if err != nil {
				return err
			}
			rows, err := q.ListTariffPools(ctx, t.ID)
			if err != nil {
				return err
			}
			settings := []PoolSetting{{PoolID: in.ID, Mode: e.Mode, TrafficLimit: e.TrafficLimit}}
			for _, r := range rows {
				if r.PoolID != in.ID {
					settings = append(settings, PoolSetting{PoolID: r.PoolID, Mode: r.Mode, TrafficLimit: store.Int64Ptr(r.TrafficLimit)})
				}
			}
			if err := setTariffPools(ctx, q, t.ID, settings); err != nil {
				return err
			}
			gifts, err := q.ListTariffGifts(ctx, t.ID)
			if err != nil {
				return err
			}
			ids := make([]int64, 0, len(gifts))
			for _, g := range gifts {
				ids = append(ids, g.PackageID)
			}
			var fe *domain.FieldError
			if err := domain.SetTariffGifts(ctx, q, t, ids); errors.As(err, &fe) {
				return fieldErrValue("body.tariffs", fe.Code, t.ID)
			} else if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.d.Changes.PoliciesChanged() // a group closed or opened for a tariff's users
	h.audit(ctx, sessionOf(ctx).AdminID, "pool.tariffs", "pool", strconv.FormatInt(in.ID, 10), map[string]any{"tariffs": len(in.Body.Tariffs)})
	return h.pool(ctx, in.ID)
}

// deletePool refuses while the pool holds what users paid for: deleting it would cascade
// to their grants and to the catalog's packages, and a paid invoice of a package would
// lose it. The traffic stays theirs; the admin waits for it to run out, or archives the
// packages first.
func (h *handlers) deletePool(ctx context.Context, in *userIDInput) (*struct{}, error) {
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		use, err := q.PoolUsage(ctx, db.PoolUsageParams{PoolID: sql.NullInt64{Int64: in.ID, Valid: true}, Now: h.d.Now().Unix()})
		if err != nil {
			return err
		}
		var details []error
		for _, c := range []struct {
			n    int64
			code string
		}{{use.Grants, "pool_has_grants"}, {use.Packages, "pool_has_packages"}, {use.Payments, "pool_has_payments"}} {
			if c.n > 0 {
				details = append(details, &huma.ErrorDetail{Location: "path.id", Message: c.code, Value: c.n})
			}
		}
		if len(details) > 0 {
			return huma.Error409Conflict("pool_in_use", details...)
		}
		promos, err := q.CountEnabledPromoCodesForPool(ctx, sql.NullInt64{Int64: in.ID, Valid: true})
		if err != nil {
			return err
		}
		if promos > 0 {
			return huma.Error409Conflict("pool_in_use", &huma.ErrorDetail{Location: "path.id", Message: "pool_has_active_promocodes", Value: promos})
		}
		if err := q.ClearDisabledPromoPools(ctx, sql.NullInt64{Int64: in.ID, Valid: true}); err != nil {
			return err
		}
		n, err := q.DeleteTrafficPool(ctx, in.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return huma.Error404NotFound("not_found")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Its inbounds count to the main quota again; nodes and policies must know.
	h.d.Changes.SlotsChanged()
	h.d.Changes.PoliciesChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "pool.delete", "pool", strconv.FormatInt(in.ID, 10), nil)
	return nil, nil
}

func (h *handlers) userPools(ctx context.Context, in *userIDInput) (*userPoolsOutput, error) {
	if err := requireUser(ctx, h.d.Store.Q, in.ID); err != nil {
		return nil, err
	}
	ps, err := h.d.Store.Q.ListTrafficPools(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := h.d.Store.Q.ListUserPools(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	grants, err := domain.UserGrantsLeft(ctx, h.d.Store.Q, in.ID, h.d.Now())
	if err != nil {
		return nil, err
	}
	out := &userPoolsOutput{Body: make([]UserPoolView, 0, len(ps))}
	for _, p := range ps {
		r, mode := domain.UserPoolOf(rows, p.ID)
		v := UserPoolView{PoolID: p.ID, Name: p.Name, Mode: mode, TrafficLimit: store.Int64Ptr(r.TrafficLimit), UsedUp: r.UsedUp, UsedDown: r.UsedDown,
			Extra: grants.Pool(in.ID, p.ID)}
		r.Mode = mode
		v.Exhausted = domain.PoolExhausted(r, v.Extra)
		out.Body = append(out.Body, v)
	}
	return out, nil
}

func (h *handlers) setUserPools(ctx context.Context, in *userPoolsInput) (*userPoolsOutput, error) {
	if err := requireUser(ctx, h.d.Store.Q, in.ID); err != nil {
		return nil, err
	}
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		if err := checkPoolSettings(ctx, q, in.Body.Pools); err != nil {
			return err
		}
		for _, p := range in.Body.Pools {
			if err := q.SetUserPool(ctx, db.SetUserPoolParams{UserID: in.ID, PoolID: p.PoolID, Mode: p.Mode, TrafficLimit: settingLimit(p)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.d.Changes.PoliciesChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "user.pools", "user", strconv.FormatInt(in.ID, 10), nil)
	return h.userPools(ctx, &userIDInput{ID: in.ID})
}

// settingLimit is the limit a setting stores: its own with the mode own, none otherwise.
func settingLimit(p PoolSetting) sql.NullInt64 {
	if p.Mode != domain.PoolOwn {
		return sql.NullInt64{}
	}
	return domain.NullInt(p.TrafficLimit)
}

// checkPoolSettings refuses a list of group settings that names a group twice or one that
// is not there, or gives the mode own no limit, before anything is written: a user and a
// tariff are held to the same list.
func checkPoolSettings(ctx context.Context, q *db.Queries, settings []PoolSetting) error {
	seen := make(map[int64]bool, len(settings))
	for _, p := range settings {
		if seen[p.PoolID] {
			return fieldErrValue("body.pools", "pool_duplicate", p.PoolID)
		}
		seen[p.PoolID] = true
		if !domain.ValidPoolMode(p.Mode) {
			return fieldErrValue("body.pools", "pool_mode", p.PoolID)
		}
		if p.Mode == domain.PoolOwn && p.TrafficLimit == nil {
			return fieldErrValue("body.pools", "pool_own_limit", p.PoolID)
		}
		switch err := domain.PoolExists(ctx, q, p.PoolID); {
		case errors.As(err, new(*domain.FieldError)):
			return fieldErrValue("body.pools", "pool_not_found", p.PoolID)
		case err != nil:
			return err
		}
	}
	return nil
}

// setTariffPools makes the tariff's groups as the list says on q's transaction; a group the
// list leaves out is in the main traffic. The list is checked whole (a group twice, an
// unknown group, own without a limit) before the old settings go. A group closed or opened
// again reaches the tariff's users at once, in its new mode, unlike the rest, which comes
// with a purchase, a change of tariff or "apply to the subscriptions given out": closing
// the expensive server to a cheap tariff is meant now. Only the groups whose closing
// changed are touched, so what the admin set on a user stays.
func setTariffPools(ctx context.Context, q *db.Queries, tariffID int64, settings []PoolSetting) error {
	if err := checkPoolSettings(ctx, q, settings); err != nil {
		return err
	}
	before, err := q.ListTariffPools(ctx, tariffID)
	if err != nil {
		return err
	}
	if err := q.ClearTariffPools(ctx, tariffID); err != nil {
		return err
	}
	now := map[int64]PoolSetting{}
	for _, p := range settings {
		now[p.PoolID] = p
		if p.Mode == domain.PoolMain {
			continue // no row: the main traffic
		}
		if err := q.AddTariffPool(ctx, db.AddTariffPoolParams{TariffID: tariffID, PoolID: p.PoolID, Mode: p.Mode, TrafficLimit: settingLimit(p)}); err != nil {
			return err
		}
	}
	// The groups whose closing changed: closed now, or closed before.
	touched := map[int64]bool{}
	for _, p := range settings {
		if was, _ := domain.TariffPoolMode(before, p.PoolID); (was == domain.PoolClosed) != (p.Mode == domain.PoolClosed) {
			touched[p.PoolID] = true
		}
	}
	for _, r := range before {
		if _, listed := now[r.PoolID]; r.Mode == domain.PoolClosed && !listed {
			touched[r.PoolID] = true
		}
	}
	for id := range touched {
		p, listed := now[id]
		if !listed {
			p = PoolSetting{PoolID: id, Mode: domain.PoolMain}
		}
		if err := q.SetTariffUsersPool(ctx, db.SetTariffUsersPoolParams{TariffID: tariffID, PoolID: id, Mode: p.Mode, TrafficLimit: settingLimit(p)}); err != nil {
			return err
		}
	}
	return nil
}

// tariffPoolsOf lists how the tariff has every group (pools), for its view: a group without
// a row of the tariff (in all) is in the main traffic.
func tariffPoolsOf(all []db.TariffPool, pools []db.TrafficPool, tariffID int64) []PoolSetting {
	var mine []db.TariffPool
	for _, r := range all {
		if r.TariffID == tariffID {
			mine = append(mine, r)
		}
	}
	out := make([]PoolSetting, 0, len(pools))
	for _, p := range pools {
		mode, limit := domain.TariffPoolMode(mine, p.ID)
		out = append(out, PoolSetting{PoolID: p.ID, Mode: mode, TrafficLimit: store.Int64Ptr(limit)})
	}
	return out
}
