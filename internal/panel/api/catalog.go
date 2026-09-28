package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/presets"
	"mikan/internal/panel/store/db"
)

type TariffView struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	TrafficLimit  *int64 `json:"traffic_limit" doc:"Байты; null — без лимита"`
	DurationDays  int64  `json:"duration_days" doc:"0 — бессрочно"`
	DeviceLimit   *int64 `json:"device_limit"`
	ResetStrategy string `json:"reset_strategy" enum:"none,month_start,period"`
	PriceLabel    string `json:"price_label"`
	Sort          int64  `json:"sort"`
}

func viewTariff(t db.Tariff) TariffView {
	return TariffView{ID: t.ID, Name: t.Name, TrafficLimit: ptrInt(t.TrafficLimit.Int64, t.TrafficLimit.Valid),
		DurationDays: t.DurationDays, DeviceLimit: ptrInt(t.DeviceLimit.Int64, t.DeviceLimit.Valid),
		ResetStrategy: t.ResetStrategy, PriceLabel: t.PriceLabel, Sort: t.Sort}
}

type tariffBody struct {
	Name          string `json:"name" minLength:"1" maxLength:"60"`
	TrafficLimit  *int64 `json:"traffic_limit,omitempty" minimum:"1"`
	DurationDays  int64  `json:"duration_days" minimum:"0" maximum:"3650"`
	DeviceLimit   *int64 `json:"device_limit,omitempty" minimum:"1" maximum:"100"`
	ResetStrategy string `json:"reset_strategy" enum:"none,month_start,period" default:"none"`
	PriceLabel    string `json:"price_label,omitempty" maxLength:"40"`
	Sort          int64  `json:"sort,omitempty"`
}

type tariffInput struct{ Body tariffBody }
type tariffUpdateInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body tariffBody
}
type tariffOutput struct{ Body TariffView }
type tariffsOutput struct{ Body []TariffView }

type InboundView struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Preset      string    `json:"preset"`
	Title       string    `json:"title"`
	Network     string    `json:"network"`
	Port        string    `json:"port"`
	Enabled     bool      `json:"enabled"`
	Dest        string    `json:"dest,omitempty" doc:"Сайт для маскировки REALITY"`
	ServerNames []string  `json:"server_names,omitempty"`
	Status      string    `json:"status" enum:"ok,error,unknown"`
	Error       string    `json:"error,omitempty"`
	Conns       int       `json:"conns"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type inboundsOutput struct{ Body []InboundView }
type inboundOutput struct{ Body InboundView }

type createInboundInput struct {
	Body struct {
		Preset string `json:"preset" enum:"vless_reality_vision,vless_reality_xhttp,hysteria2,tuic_v5"`
		Port   string `json:"port,omitempty" pattern:"^[0-9]{1,5}(-[0-9]{1,5})?$"`
		Dest   string `json:"dest,omitempty" maxLength:"255" doc:"host:port для REALITY"`
	}
}

type patchInboundInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Port    *string `json:"port,omitempty" pattern:"^[0-9]{1,5}(-[0-9]{1,5})?$"`
		Enabled *bool   `json:"enabled,omitempty"`
		Dest    *string `json:"dest,omitempty" maxLength:"255"`
	}
}

type presetsOutput struct{ Body []presets.Info }

func (h *handlers) registerCatalog() {
	huma.Register(h.api, huma.Operation{OperationID: "list-tariffs", Method: http.MethodGet, Path: "/api/v1/tariffs", Summary: "Тарифы", Tags: []string{"tariffs"}}, h.listTariffs)
	huma.Register(h.api, huma.Operation{OperationID: "create-tariff", Method: http.MethodPost, Path: "/api/v1/tariffs", Summary: "Создать тариф", Tags: []string{"tariffs"}, DefaultStatus: http.StatusCreated}, h.createTariff)
	huma.Register(h.api, huma.Operation{OperationID: "update-tariff", Method: http.MethodPut, Path: "/api/v1/tariffs/{id}", Summary: "Изменить тариф", Tags: []string{"tariffs"}}, h.updateTariff)
	huma.Register(h.api, huma.Operation{OperationID: "archive-tariff", Method: http.MethodDelete, Path: "/api/v1/tariffs/{id}", Summary: "Убрать тариф в архив", Tags: []string{"tariffs"}, DefaultStatus: http.StatusNoContent}, h.archiveTariff)
	huma.Register(h.api, huma.Operation{OperationID: "list-presets", Method: http.MethodGet, Path: "/api/v1/presets", Summary: "Доступные пресеты подключений", Tags: []string{"inbounds"}}, h.listPresets)
	huma.Register(h.api, huma.Operation{OperationID: "list-inbounds", Method: http.MethodGet, Path: "/api/v1/inbounds", Summary: "Подключения", Tags: []string{"inbounds"}}, h.listInbounds)
	huma.Register(h.api, huma.Operation{OperationID: "create-inbound", Method: http.MethodPost, Path: "/api/v1/inbounds", Summary: "Добавить подключение из пресета", Tags: []string{"inbounds"}, DefaultStatus: http.StatusCreated}, h.createInbound)
	huma.Register(h.api, huma.Operation{OperationID: "update-inbound", Method: http.MethodPatch, Path: "/api/v1/inbounds/{id}", Summary: "Изменить подключение", Tags: []string{"inbounds"}}, h.updateInbound)
	huma.Register(h.api, huma.Operation{OperationID: "delete-inbound", Method: http.MethodDelete, Path: "/api/v1/inbounds/{id}", Summary: "Удалить подключение", Tags: []string{"inbounds"}, DefaultStatus: http.StatusNoContent}, h.deleteInbound)
}

func (h *handlers) listTariffs(ctx context.Context, _ *struct{}) (*tariffsOutput, error) {
	rows, err := h.d.Store.Q.ListTariffs(ctx)
	if err != nil {
		return nil, err
	}
	out := &tariffsOutput{Body: make([]TariffView, 0, len(rows))}
	for _, t := range rows {
		out.Body = append(out.Body, viewTariff(t))
	}
	return out, nil
}

func nullable(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func (h *handlers) createTariff(ctx context.Context, in *tariffInput) (*tariffOutput, error) {
	b := in.Body
	t, err := h.d.Store.Q.CreateTariff(ctx, db.CreateTariffParams{Name: strings.TrimSpace(b.Name), TrafficLimit: nullable(b.TrafficLimit),
		DurationDays: b.DurationDays, DeviceLimit: nullable(b.DeviceLimit), ResetStrategy: b.ResetStrategy, PriceLabel: b.PriceLabel,
		Sort: b.Sort, CreatedAt: h.d.Now().Unix()})
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "tariff.create", "tariff", strconv.FormatInt(t.ID, 10), nil)
	return &tariffOutput{Body: viewTariff(t)}, nil
}

func (h *handlers) updateTariff(ctx context.Context, in *tariffUpdateInput) (*tariffOutput, error) {
	b := in.Body
	t, err := h.d.Store.Q.UpdateTariff(ctx, db.UpdateTariffParams{Name: strings.TrimSpace(b.Name), TrafficLimit: nullable(b.TrafficLimit),
		DurationDays: b.DurationDays, DeviceLimit: nullable(b.DeviceLimit), ResetStrategy: b.ResetStrategy, PriceLabel: b.PriceLabel,
		Sort: b.Sort, ID: in.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("not_found")
	}
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "tariff.update", "tariff", strconv.FormatInt(t.ID, 10), nil)
	return &tariffOutput{Body: viewTariff(t)}, nil
}

func (h *handlers) archiveTariff(ctx context.Context, in *userIDInput) (*struct{}, error) {
	if err := h.d.Store.Q.ArchiveTariff(ctx, in.ID); err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "tariff.archive", "tariff", strconv.FormatInt(in.ID, 10), nil)
	return nil, nil
}

func (h *handlers) listPresets(context.Context, *struct{}) (*presetsOutput, error) {
	return &presetsOutput{Body: presets.All}, nil
}

func (h *handlers) viewInbound(in db.Inbound) InboundView {
	info, _ := presets.Get(in.Preset)
	v := InboundView{ID: in.ID, Name: in.Name, Preset: in.Preset, Title: info.Title, Network: info.Network, Port: in.Port,
		Enabled: in.Enabled != 0, Status: "unknown", UpdatedAt: time.Unix(in.UpdatedAt, 0).UTC()}
	if r, err := presets.Reality(in.Preset, []byte(in.Settings)); err == nil {
		v.Dest, v.ServerNames = r.Dest, r.ServerNames
	}
	if h.d.Listeners != nil {
		for _, l := range h.d.Listeners() {
			if l.Name == in.Name {
				v.Status, v.Error = "ok", ""
				if !l.OK {
					v.Status, v.Error = "error", l.Error
				}
			}
		}
	}
	return v
}

func (h *handlers) listInbounds(ctx context.Context, _ *struct{}) (*inboundsOutput, error) {
	rows, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	out := &inboundsOutput{Body: make([]InboundView, 0, len(rows))}
	for _, in := range rows {
		out.Body = append(out.Body, h.viewInbound(in))
	}
	return out, nil
}

func validPort(spec string) bool {
	lo, hi, isRange := strings.Cut(spec, "-")
	a, err := strconv.Atoi(lo)
	if err != nil || a < 1 || a > 65535 {
		return false
	}
	if !isRange {
		return true
	}
	b, err := strconv.Atoi(hi)
	return err == nil && b > a && b <= 65535
}

func (h *handlers) createInbound(ctx context.Context, in *createInboundInput) (*inboundOutput, error) {
	info, ok := presets.Get(in.Body.Preset)
	if !ok {
		return nil, huma.Error422UnprocessableEntity("unknown_preset")
	}
	port := in.Body.Port
	if port == "" {
		port = info.Port
	}
	if !validPort(port) {
		return nil, huma.Error422UnprocessableEntity("bad_port", &huma.ErrorDetail{Location: "body.port", Message: "Порт 1–65535 или диапазон вида 20000-20100"})
	}
	existing, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	name := info.Name
	taken := map[string]bool{}
	for _, e := range existing {
		taken[e.Name] = true
		eInfo, _ := presets.Get(e.Preset)
		if e.Port == port && eInfo.Network == info.Network && e.Enabled != 0 {
			return nil, huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "Этот порт уже занят подключением «" + e.Name + "»"})
		}
	}
	for i := 2; taken[name]; i++ {
		name = info.Name + "-" + strconv.Itoa(i)
	}
	settings, err := presets.NewSettings(info.ID, in.Body.Dest)
	if err != nil {
		return nil, err
	}
	now := h.d.Now().Unix()
	row, err := h.d.Store.Q.CreateInbound(ctx, db.CreateInboundParams{Name: name, Preset: info.ID, Port: port, Settings: string(settings), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "inbound.create", "inbound", name, map[string]any{"preset": info.ID, "port": port})
	return &inboundOutput{Body: h.viewInbound(row)}, nil
}

func (h *handlers) updateInbound(ctx context.Context, in *patchInboundInput) (*inboundOutput, error) {
	row, err := h.d.Store.Q.GetInbound(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("not_found")
	}
	if err != nil {
		return nil, err
	}
	port, enabled, settings := row.Port, row.Enabled, row.Settings
	if in.Body.Port != nil {
		if !validPort(*in.Body.Port) {
			return nil, huma.Error422UnprocessableEntity("bad_port", &huma.ErrorDetail{Location: "body.port", Message: "Порт 1–65535 или диапазон"})
		}
		port = *in.Body.Port
	}
	if in.Body.Enabled != nil {
		enabled = 0
		if *in.Body.Enabled {
			enabled = 1
		}
	}
	if in.Body.Dest != nil {
		if settings, err = withDest(row.Preset, settings, *in.Body.Dest); err != nil {
			return nil, huma.Error422UnprocessableEntity("bad_dest", &huma.ErrorDetail{Location: "body.dest", Message: err.Error()})
		}
	}
	row, err = h.d.Store.Q.UpdateInbound(ctx, db.UpdateInboundParams{Port: port, Enabled: enabled, Settings: settings, UpdatedAt: h.d.Now().Unix(), ID: in.ID})
	if err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "inbound.update", "inbound", row.Name, nil)
	return &inboundOutput{Body: h.viewInbound(row)}, nil
}

func withDest(preset, settings, dest string) (string, error) {
	host, port, ok := strings.Cut(dest, ":")
	if !ok || host == "" || !validPort(port) || strings.Contains(port, "-") {
		return "", errors.New("Укажите сайт в виде host:port, например www.microsoft.com:443")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(settings), &m); err != nil {
		return "", err
	}
	r, ok := m["reality"].(map[string]any)
	if !ok {
		return "", errors.New("У этого подключения нет маскировки REALITY")
	}
	r["dest"] = dest
	r["server_names"] = []string{host}
	raw, err := json.Marshal(m)
	return string(raw), err
}

func (h *handlers) deleteInbound(ctx context.Context, in *userIDInput) (*struct{}, error) {
	row, err := h.d.Store.Q.GetInbound(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("not_found")
	}
	if err != nil {
		return nil, err
	}
	if err := h.d.Store.Q.DeleteInbound(ctx, in.ID); err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "inbound.delete", "inbound", row.Name, nil)
	return nil, nil
}
