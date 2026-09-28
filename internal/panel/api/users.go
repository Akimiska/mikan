package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

type UserView struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Contact       string     `json:"contact"`
	Note          string     `json:"note"`
	Tags          []string   `json:"tags"`
	State         string     `json:"state" enum:"active,expiring,limited,expired,disabled"`
	TariffID      *int64     `json:"tariff_id"`
	TrafficLimit  *int64     `json:"traffic_limit" doc:"Байты за период; null — без лимита"`
	UsedUp        int64      `json:"used_up"`
	UsedDown      int64      `json:"used_down"`
	TotalUp       int64      `json:"total_up"`
	TotalDown     int64      `json:"total_down"`
	DeviceLimit   *int64     `json:"device_limit"`
	ResetStrategy string     `json:"reset_strategy" enum:"none,month_start,period"`
	ResetsAt      *time.Time `json:"resets_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	Inbounds      []int64    `json:"inbounds" doc:"Разрешённые подключения; пусто — все"`
	SubURL        string     `json:"sub_url"`
	Online        bool       `json:"online"`
	OnlineIPs     []string   `json:"online_ips"`
	OnlineAt      *time.Time `json:"online_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func ptrInt(v int64, ok bool) *int64 {
	if !ok {
		return nil
	}
	return &v
}

func ptrTime(v int64, ok bool) *time.Time {
	if !ok {
		return nil
	}
	t := time.Unix(v, 0).UTC()
	return &t
}

func (h *handlers) viewUser(ctx context.Context, u db.User, slotName map[int64]string) UserView {
	now := h.d.Now()
	v := UserView{
		ID: u.ID, Name: u.Name, Contact: u.Contact, Note: u.Note, Tags: domain.DecodeTags(u.Tags),
		State: domain.State(u, now), TariffID: ptrInt(u.TariffID.Int64, u.TariffID.Valid),
		TrafficLimit: ptrInt(u.TrafficLimit.Int64, u.TrafficLimit.Valid), UsedUp: u.UsedUp, UsedDown: u.UsedDown,
		TotalUp: u.TotalUp, TotalDown: u.TotalDown, DeviceLimit: ptrInt(u.DeviceLimit.Int64, u.DeviceLimit.Valid),
		ResetStrategy: u.ResetStrategy, ExpiresAt: ptrTime(u.ExpiresAt.Int64, u.ExpiresAt.Valid),
		Inbounds: domain.DecodeInbounds(u.Inbounds), OnlineAt: ptrTime(u.OnlineAt.Int64, u.OnlineAt.Valid),
		CreatedAt: time.Unix(u.CreatedAt, 0).UTC(), OnlineIPs: []string{},
	}
	if v.Inbounds == nil {
		v.Inbounds = []int64{}
	}
	if t, ok := domain.NextReset(u, now); ok {
		v.ResetsAt = &t
	}
	if h.d.SubURL != nil {
		v.SubURL = h.d.SubURL(ctx, u.SubToken)
	}
	if u.SlotID.Valid && h.d.Online != nil {
		if on, ok := h.d.Online()[slotName[u.SlotID.Int64]]; ok {
			v.Online = on.Conns > 0 || len(on.IPs) > 0
			v.OnlineIPs = on.IPs
		}
	}
	return v
}

func (h *handlers) slotNames(ctx context.Context) (map[int64]string, error) {
	slots, err := h.d.Store.Q.ListSlots(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(slots))
	for _, s := range slots {
		m[s.ID] = s.Name
	}
	return m, nil
}

type listUsersInput struct {
	State  string `query:"state" enum:"all,active,expiring,limited,expired,disabled" default:"all"`
	Query  string `query:"q" maxLength:"100"`
	Limit  int    `query:"limit" minimum:"1" maximum:"500" default:"100"`
	Offset int    `query:"offset" minimum:"0" default:"0"`
}

type UserCounts struct {
	All      int `json:"all"`
	Active   int `json:"active"`
	Expiring int `json:"expiring"`
	Limited  int `json:"limited"`
	Expired  int `json:"expired"`
	Disabled int `json:"disabled"`
}

type listUsersOutput struct {
	Body struct {
		Items  []UserView `json:"items"`
		Total  int        `json:"total" doc:"Сколько подходит под фильтр"`
		Counts UserCounts `json:"counts"`
	}
}

type userOutput struct{ Body UserView }

type userIDInput struct {
	ID int64 `path:"id" minimum:"1"`
}

type createUserInput struct {
	Body struct {
		Name     string   `json:"name" minLength:"1" maxLength:"100"`
		Contact  string   `json:"contact,omitempty" maxLength:"100"`
		Note     string   `json:"note,omitempty" maxLength:"2000"`
		Tags     []string `json:"tags,omitempty" maxItems:"20"`
		TariffID int64    `json:"tariff_id" minimum:"1"`
	}
}

type patchUserInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Name             *string    `json:"name,omitempty" minLength:"1" maxLength:"100"`
		Contact          *string    `json:"contact,omitempty" maxLength:"100"`
		Note             *string    `json:"note,omitempty" maxLength:"2000"`
		Tags             *[]string  `json:"tags,omitempty" maxItems:"20"`
		Disabled         *bool      `json:"disabled,omitempty"`
		TrafficLimit     *int64     `json:"traffic_limit,omitempty" minimum:"0"`
		TrafficUnlimited bool       `json:"traffic_unlimited,omitempty"`
		DeviceLimit      *int64     `json:"device_limit,omitempty" minimum:"1" maximum:"100"`
		DevicesUnlimited bool       `json:"devices_unlimited,omitempty"`
		ExpiresAt        *time.Time `json:"expires_at,omitempty"`
		NeverExpires     bool       `json:"never_expires,omitempty"`
		Inbounds         *[]int64   `json:"inbounds,omitempty"`
		TariffID         *int64     `json:"tariff_id,omitempty" minimum:"1" doc:"Применить тариф: лимиты из тарифа, срок — от сегодня"`
	}
}

type extendInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Days int64 `json:"days" minimum:"1" maximum:"3650"`
	}
}

type bulkInput struct {
	Body struct {
		IDs    []int64 `json:"ids" minItems:"1" maxItems:"1000"`
		Action string  `json:"action" enum:"extend,reset,disable,enable,delete"`
		Days   int64   `json:"days,omitempty" minimum:"0" maximum:"3650"`
	}
}

type bulkOutput struct {
	Body struct {
		Affected int `json:"affected"`
	}
}

type trafficInput struct {
	ID    int64  `path:"id" minimum:"1"`
	Range string `query:"range" enum:"24h,7d,30d" default:"7d"`
}

type TrafficPoint struct {
	T    time.Time `json:"t"`
	Up   int64     `json:"up"`
	Down int64     `json:"down"`
}

type trafficOutput struct {
	Body struct {
		Points []TrafficPoint `json:"points"`
	}
}

type DeviceView struct {
	IP        string    `json:"ip"`
	Client    string    `json:"client"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Online    bool      `json:"online"`
}

type devicesOutput struct{ Body []DeviceView }

func (h *handlers) registerUsers() {
	tags := []string{"users"}
	huma.Register(h.api, huma.Operation{OperationID: "list-users", Method: http.MethodGet, Path: "/api/v1/users", Summary: "Список пользователей", Tags: tags}, h.listUsers)
	huma.Register(h.api, huma.Operation{OperationID: "create-user", Method: http.MethodPost, Path: "/api/v1/users", Summary: "Создать пользователя по тарифу", Tags: tags, DefaultStatus: http.StatusCreated}, h.createUser)
	huma.Register(h.api, huma.Operation{OperationID: "get-user", Method: http.MethodGet, Path: "/api/v1/users/{id}", Summary: "Пользователь", Tags: tags}, h.getUser)
	huma.Register(h.api, huma.Operation{OperationID: "update-user", Method: http.MethodPatch, Path: "/api/v1/users/{id}", Summary: "Изменить пользователя", Tags: tags}, h.updateUser)
	huma.Register(h.api, huma.Operation{OperationID: "delete-user", Method: http.MethodDelete, Path: "/api/v1/users/{id}", Summary: "Удалить пользователя", Tags: tags, DefaultStatus: http.StatusNoContent}, h.deleteUser)
	huma.Register(h.api, huma.Operation{OperationID: "extend-user", Method: http.MethodPost, Path: "/api/v1/users/{id}/extend", Summary: "Продлить", Tags: tags}, h.extendUser)
	huma.Register(h.api, huma.Operation{OperationID: "reset-user-traffic", Method: http.MethodPost, Path: "/api/v1/users/{id}/reset-traffic", Summary: "Сбросить трафик", Tags: tags}, h.resetUserTraffic)
	huma.Register(h.api, huma.Operation{OperationID: "reissue-user", Method: http.MethodPost, Path: "/api/v1/users/{id}/reissue", Summary: "Перевыпустить ссылку", Tags: tags}, h.reissueUser)
	huma.Register(h.api, huma.Operation{OperationID: "bulk-users", Method: http.MethodPost, Path: "/api/v1/users/bulk", Summary: "Массовое действие", Tags: tags}, h.bulkUsers)
	huma.Register(h.api, huma.Operation{OperationID: "user-traffic", Method: http.MethodGet, Path: "/api/v1/users/{id}/traffic", Summary: "График трафика пользователя", Tags: tags}, h.userTraffic)
	huma.Register(h.api, huma.Operation{OperationID: "user-devices", Method: http.MethodGet, Path: "/api/v1/users/{id}/devices", Summary: "Устройства пользователя", Tags: tags}, h.userDevices)
}

func mapDomainErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("not_found")
	case errors.Is(err, domain.ErrNoSlots):
		return huma.Error503ServiceUnavailable("no_free_slots")
	}
	return err
}

func (h *handlers) listUsers(ctx context.Context, in *listUsersInput) (*listUsersOutput, error) {
	users, err := h.d.Store.Q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	names, err := h.slotNames(ctx)
	if err != nil {
		return nil, err
	}
	now := h.d.Now()
	q := strings.ToLower(strings.TrimSpace(in.Query))
	out := &listUsersOutput{}
	var matched []db.User
	for _, u := range users {
		st := domain.State(u, now)
		c := &out.Body.Counts
		c.All++
		switch st {
		case domain.StateActive:
			c.Active++
		case domain.StateExpiring:
			c.Expiring++
			c.Active++
		case domain.StateLimited:
			c.Limited++
		case domain.StateExpired:
			c.Expired++
		case domain.StateDisabled:
			c.Disabled++
		}
		if in.State != "all" && !(st == in.State || (in.State == domain.StateActive && st == domain.StateExpiring)) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(u.Name+" "+u.Contact+" "+u.Note+" "+u.Tags), q) {
			continue
		}
		matched = append(matched, u)
	}
	out.Body.Total = len(matched)
	end := min(in.Offset+in.Limit, len(matched))
	out.Body.Items = []UserView{}
	if in.Offset < len(matched) {
		for _, u := range matched[in.Offset:end] {
			out.Body.Items = append(out.Body.Items, h.viewUser(ctx, u, names))
		}
	}
	return out, nil
}

func (h *handlers) userResult(ctx context.Context, u db.User, err error) (*userOutput, error) {
	if err != nil {
		return nil, mapDomainErr(err)
	}
	names, err := h.slotNames(ctx)
	if err != nil {
		return nil, err
	}
	return &userOutput{Body: h.viewUser(ctx, u, names)}, nil
}

func (h *handlers) createUser(ctx context.Context, in *createUserInput) (*userOutput, error) {
	u, err := h.d.Users.Create(ctx, domain.CreateInput{Name: in.Body.Name, Contact: in.Body.Contact, Note: in.Body.Note, Tags: in.Body.Tags, TariffID: in.Body.TariffID})
	if errors.Is(err, domain.ErrNotFound) {
		return nil, huma.Error422UnprocessableEntity("tariff_not_found", &huma.ErrorDetail{Location: "body.tariff_id", Message: "Тариф не найден"})
	}
	if err == nil {
		h.audit(ctx, sessionOf(ctx).AdminID, "user.create", "user", strconv.FormatInt(u.ID, 10), map[string]any{"tariff_id": in.Body.TariffID})
	}
	return h.userResult(ctx, u, err)
}

func (h *handlers) getUser(ctx context.Context, in *userIDInput) (*userOutput, error) {
	u, err := h.d.Users.Get(ctx, in.ID)
	return h.userResult(ctx, u, err)
}

func (h *handlers) updateUser(ctx context.Context, in *patchUserInput) (*userOutput, error) {
	b := in.Body
	p := domain.Patch{
		Name: b.Name, Contact: b.Contact, Note: b.Note, Tags: b.Tags, Disabled: b.Disabled,
		TrafficLimit: b.TrafficLimit, ClearTrafficLimit: b.TrafficUnlimited,
		DeviceLimit: b.DeviceLimit, ClearDeviceLimit: b.DevicesUnlimited,
		ExpiresAt: b.ExpiresAt, ClearExpiry: b.NeverExpires, Inbounds: b.Inbounds, TariffID: b.TariffID,
	}
	u, err := h.d.Users.Update(ctx, in.ID, p)
	if err == nil {
		h.audit(ctx, sessionOf(ctx).AdminID, "user.update", "user", strconv.FormatInt(in.ID, 10), nil)
	}
	return h.userResult(ctx, u, err)
}

func (h *handlers) deleteUser(ctx context.Context, in *userIDInput) (*struct{}, error) {
	if err := h.d.Users.Delete(ctx, in.ID); err != nil {
		return nil, mapDomainErr(err)
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "user.delete", "user", strconv.FormatInt(in.ID, 10), nil)
	return nil, nil
}

func (h *handlers) extendUser(ctx context.Context, in *extendInput) (*userOutput, error) {
	u, err := h.d.Users.Extend(ctx, in.ID, in.Body.Days)
	if err == nil {
		h.audit(ctx, sessionOf(ctx).AdminID, "user.extend", "user", strconv.FormatInt(in.ID, 10), map[string]any{"days": in.Body.Days})
	}
	return h.userResult(ctx, u, err)
}

func (h *handlers) resetUserTraffic(ctx context.Context, in *userIDInput) (*userOutput, error) {
	u, err := h.d.Users.ResetTraffic(ctx, in.ID)
	if err == nil {
		h.audit(ctx, sessionOf(ctx).AdminID, "user.reset_traffic", "user", strconv.FormatInt(in.ID, 10), nil)
	}
	return h.userResult(ctx, u, err)
}

func (h *handlers) reissueUser(ctx context.Context, in *userIDInput) (*userOutput, error) {
	u, err := h.d.Users.Reissue(ctx, in.ID)
	if err == nil {
		h.audit(ctx, sessionOf(ctx).AdminID, "user.reissue", "user", strconv.FormatInt(in.ID, 10), nil)
	}
	return h.userResult(ctx, u, err)
}

func (h *handlers) bulkUsers(ctx context.Context, in *bulkInput) (*bulkOutput, error) {
	out := &bulkOutput{}
	for _, id := range in.Body.IDs {
		var err error
		switch in.Body.Action {
		case "extend":
			days := in.Body.Days
			if days == 0 {
				days = 30
			}
			_, err = h.d.Users.Extend(ctx, id, days)
		case "reset":
			_, err = h.d.Users.ResetTraffic(ctx, id)
		case "disable", "enable":
			v := in.Body.Action == "disable"
			_, err = h.d.Users.Update(ctx, id, domain.Patch{Disabled: &v})
		case "delete":
			err = h.d.Users.Delete(ctx, id)
		}
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out.Body.Affected++
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "user.bulk_"+in.Body.Action, "user", "", map[string]any{"count": out.Body.Affected})
	return out, nil
}

func (h *handlers) userTraffic(ctx context.Context, in *trafficInput) (*trafficOutput, error) {
	now := h.d.Now()
	out := &trafficOutput{}
	out.Body.Points = []TrafficPoint{}
	if in.Range == "30d" {
		from := now.Add(-30*24*time.Hour).Unix() / 86400
		rows, err := h.d.Store.Q.UserTrafficDaily(ctx, db.UserTrafficDailyParams{UserID: in.ID, Day: from})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out.Body.Points = append(out.Body.Points, TrafficPoint{T: time.Unix(r.Day*86400, 0).UTC(), Up: r.Up, Down: r.Down})
		}
		return out, nil
	}
	span := 24 * time.Hour
	if in.Range == "7d" {
		span = 7 * 24 * time.Hour
	}
	rows, err := h.d.Store.Q.UserTrafficHourly(ctx, db.UserTrafficHourlyParams{UserID: in.ID, Hour: now.Add(-span).Unix() / 3600})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out.Body.Points = append(out.Body.Points, TrafficPoint{T: time.Unix(r.Hour*3600, 0).UTC(), Up: r.Up, Down: r.Down})
	}
	return out, nil
}

func (h *handlers) userDevices(ctx context.Context, in *userIDInput) (*devicesOutput, error) {
	u, err := h.d.Users.Get(ctx, in.ID)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	rows, err := h.d.Store.Q.ListUserDevices(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	var live []string
	if u.SlotID.Valid && h.d.Online != nil {
		names, err := h.slotNames(ctx)
		if err != nil {
			return nil, err
		}
		live = h.d.Online()[names[u.SlotID.Int64]].IPs
	}
	out := &devicesOutput{Body: []DeviceView{}}
	for _, d := range rows {
		out.Body = append(out.Body, DeviceView{IP: d.Ip, Client: d.Client, FirstSeen: time.Unix(d.FirstSeen, 0).UTC(), LastSeen: time.Unix(d.LastSeen, 0).UTC(), Online: slices.Contains(live, d.Ip)})
	}
	return out, nil
}
