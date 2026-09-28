package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/presets"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/subs"
	"mikan/internal/proto"
)

type InboundView struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Preset      string    `json:"preset"`
	Title       string    `json:"title"`
	Type        string    `json:"type" doc:"Тип листенера mihomo"`
	Network     string    `json:"network"`
	Port        string    `json:"port"`
	Enabled     bool      `json:"enabled"`
	DisplayName string    `json:"display_name" doc:"Своё имя в подписке; пусто — имя по умолчанию"`
	SubName     string    `json:"sub_name" doc:"Имя, которое увидит клиент"`
	Config      string    `json:"config" doc:"Шаблон листенера (YAML)"`
	Dest        string    `json:"dest,omitempty" doc:"Сайт для маскировки REALITY"`
	ServerNames []string  `json:"server_names,omitempty"`
	Status      string    `json:"status" enum:"ok,error,unknown"`
	Error       string    `json:"error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type inboundsOutput struct{ Body []InboundView }
type inboundOutput struct{ Body InboundView }

type createInboundInput struct {
	Body struct {
		Preset string `json:"preset" enum:"vless_reality_xhttp,hysteria2,tuic_v5,vless_reality_vision,vless_reality_grpc,trojan_reality,anytls,custom"`
		Port   string `json:"port,omitempty" pattern:"^[0-9]{1,5}(-[0-9]{1,5})?$"`
		Dest   string `json:"dest,omitempty" maxLength:"255" doc:"host:port для REALITY"`
		Config string `json:"config,omitempty" maxLength:"65536" doc:"Шаблон листенера (YAML) для preset=custom"`
	}
}

type patchInboundInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Port        *string `json:"port,omitempty" pattern:"^[0-9]{1,5}(-[0-9]{1,5})?$"`
		Enabled     *bool   `json:"enabled,omitempty"`
		Dest        *string `json:"dest,omitempty" maxLength:"255"`
		ServerName  *string `json:"server_name,omitempty" maxLength:"253" doc:"SNI для клиентов, если dest — IP (цель из подбора соседей)"`
		DisplayName *string `json:"display_name,omitempty" maxLength:"200" doc:"Можно с эмодзи: «🇳🇱 Нидерланды». Пусто — имя по умолчанию"`
		Config      *string `json:"config,omitempty" maxLength:"65536" doc:"Шаблон листенера (YAML)"`
	}
}

type validateInboundInput struct {
	Body struct {
		Config string `json:"config" maxLength:"65536"`
		Port   string `json:"port,omitempty" pattern:"^[0-9]{1,5}(-[0-9]{1,5})?$"`
	}
}

type validateInboundOutput struct {
	Body struct {
		Type    string `json:"type"`
		Network string `json:"network"`
	}
}

type presetsOutput struct{ Body []presets.Info }

func (h *handlers) registerInbounds() {
	huma.Register(h.api, huma.Operation{OperationID: "list-presets", Method: http.MethodGet, Path: "/api/v1/presets", Summary: "Доступные пресеты подключений", Tags: []string{"inbounds"}}, h.listPresets)
	huma.Register(h.api, huma.Operation{OperationID: "list-inbounds", Method: http.MethodGet, Path: "/api/v1/inbounds", Summary: "Подключения", Tags: []string{"inbounds"}}, h.listInbounds)
	huma.Register(h.api, huma.Operation{OperationID: "create-inbound", Method: http.MethodPost, Path: "/api/v1/inbounds", Summary: "Добавить подключение", Tags: []string{"inbounds"}, DefaultStatus: http.StatusCreated}, h.createInbound)
	huma.Register(h.api, huma.Operation{OperationID: "validate-inbound", Method: http.MethodPost, Path: "/api/v1/inbounds/validate", Summary: "Проверить шаблон листенера без сохранения", Tags: []string{"inbounds"}}, h.validateInbound)
	huma.Register(h.api, huma.Operation{OperationID: "update-inbound", Method: http.MethodPatch, Path: "/api/v1/inbounds/{id}", Summary: "Изменить подключение", Tags: []string{"inbounds"}}, h.updateInbound)
	huma.Register(h.api, huma.Operation{OperationID: "delete-inbound", Method: http.MethodDelete, Path: "/api/v1/inbounds/{id}", Summary: "Удалить подключение", Tags: []string{"inbounds"}, DefaultStatus: http.StatusNoContent}, h.deleteInbound)
}

func (h *handlers) listPresets(context.Context, *struct{}) (*presetsOutput, error) {
	return &presetsOutput{Body: presets.All}, nil
}

func (h *handlers) viewInbound(in db.Inbound) InboundView {
	info, _ := presets.Get(in.Preset)
	v := InboundView{ID: in.ID, Name: in.Name, Preset: in.Preset, Title: info.Title, Port: in.Port, Enabled: in.Enabled != 0,
		DisplayName: in.DisplayName, SubName: subs.ProxyName(in), Config: in.Config, Status: "unknown", UpdatedAt: time.Unix(in.UpdatedAt, 0).UTC()}
	if t, err := proto.Parse(in.Config); err == nil {
		v.Type, v.Network = t.Type(), t.Network()
		v.Dest, v.ServerNames = presets.Dest(t)
		if in.Preset == presets.Custom {
			v.Title = t.Type()
		}
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

// checkConfig parses and validates a template: mikan's rules first, then mihomo's own
// parser on the node, so a broken template never replaces a working listener.
func (h *handlers) checkConfig(ctx context.Context, config, port string) (proto.Template, error) {
	t, err := proto.Parse(config)
	if err == nil {
		var panelPort int
		if panelPort, _, err = settings.Get[int](ctx, h.d.Settings, settings.KeyPanelPort); err != nil {
			return nil, err
		}
		err = proto.Validate(t, proto.Options{SelfStealPort: panelPort})
		if err == nil && h.d.NodeValidate != nil {
			err = h.d.NodeValidate(ctx, nodeapi.ValidateRequest{Inbound: nodeapi.Inbound{Name: "validate", Port: port, Config: t.JSON()}, SelfStealPort: panelPort})
			// The node validates again on apply; when it is down, saving still works.
			if errors.Is(err, nodeapi.ErrUnavailable) {
				err = nil
			}
		}
	}
	if err == nil {
		return t, nil
	}
	var pe *proto.Error
	if errors.As(err, &pe) {
		value := pe.Field
		if pe.Detail != "" {
			value = pe.Detail
		}
		return nil, huma.Error422UnprocessableEntity("invalid_config", &huma.ErrorDetail{Location: "body.config", Message: pe.Code, Value: value})
	}
	var ne *nodeapi.Error
	if errors.As(err, &ne) {
		return nil, huma.Error422UnprocessableEntity("invalid_config", &huma.ErrorDetail{Location: "body.config", Message: "config_mihomo", Value: ne.Message})
	}
	return nil, err
}

// network of a stored inbound; two listeners may share a port only on different networks.
func (h *handlers) validateInbound(ctx context.Context, in *validateInboundInput) (*validateInboundOutput, error) {
	port := in.Body.Port
	if port == "" {
		port = "443"
	}
	t, err := h.checkConfig(ctx, in.Body.Config, port)
	if err != nil {
		return nil, err
	}
	out := &validateInboundOutput{}
	out.Body.Type, out.Body.Network = t.Type(), t.Network()
	return out, nil
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
	if !domain.ValidPort(port) {
		return nil, huma.Error422UnprocessableEntity("bad_port", &huma.ErrorDetail{Location: "body.port", Message: "bad_port"})
	}
	config := in.Body.Config
	if info.ID != presets.Custom {
		var err error
		if config, err = presets.NewConfig(info.ID, in.Body.Dest); err != nil {
			return nil, err
		}
	}
	t, err := h.checkConfig(ctx, config, port)
	if err != nil {
		return nil, err
	}
	existing, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	base := info.Name
	if info.ID == presets.Custom {
		base = t.Type()
	}
	if owner, busy := domain.PortOwner(existing, port, t.Network(), 0); busy {
		return nil, huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "port_in_use", Value: owner.Name})
	}
	name := domain.FreeName(existing, base)
	now := h.d.Now().Unix()
	row, err := h.d.Store.Q.CreateInbound(ctx, db.CreateInboundParams{Name: name, Preset: info.ID, Port: port, Config: config, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "inbound.create", "inbound", name, map[string]any{"preset": info.ID, "type": t.Type(), "port": port})
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
	b := in.Body
	port, enabled, config, display := row.Port, row.Enabled, row.Config, row.DisplayName
	if b.Port != nil {
		if !domain.ValidPort(*b.Port) {
			return nil, huma.Error422UnprocessableEntity("bad_port", &huma.ErrorDetail{Location: "body.port", Message: "bad_port"})
		}
		port = *b.Port
	}
	if b.Enabled != nil {
		enabled = 0
		if *b.Enabled {
			enabled = 1
		}
	}
	if b.Config != nil {
		config = *b.Config
	}
	if b.Dest != nil {
		t, err := proto.Parse(config)
		if err == nil {
			sni := ""
			if b.ServerName != nil {
				sni = strings.TrimSpace(*b.ServerName)
			}
			err = presets.SetDest(t, strings.TrimSpace(*b.Dest), sni)
		}
		var pe *proto.Error
		if errors.As(err, &pe) {
			return nil, huma.Error422UnprocessableEntity("bad_dest", &huma.ErrorDetail{Location: "body.dest", Message: pe.Code})
		}
		if err != nil {
			return nil, err
		}
		config = proto.Marshal(t)
	}
	if b.DisplayName != nil {
		display = strings.TrimSpace(*b.DisplayName)
	}
	t, err := h.checkConfig(ctx, config, port)
	if err != nil {
		if b.Dest != nil {
			// The simple form edits dest only; report the error on that field.
			var he huma.StatusError
			if errors.As(err, &he) {
				return nil, huma.Error422UnprocessableEntity("bad_dest", &huma.ErrorDetail{Location: "body.dest", Message: detailCode(err)})
			}
		}
		return nil, err
	}
	existing, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	next := row
	next.DisplayName = display
	if owner, busy := domain.PortOwner(existing, port, t.Network(), row.ID); busy && enabled != 0 {
		return nil, huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "port_in_use", Value: owner.Name})
	}
	for _, e := range existing {
		if e.ID == row.ID {
			continue
		}
		if b.DisplayName != nil && strings.EqualFold(subs.ProxyName(e), subs.ProxyName(next)) {
			return nil, huma.Error409Conflict("name_in_use", &huma.ErrorDetail{Location: "body.display_name", Message: "name_in_use", Value: e.Name})
		}
	}
	if b.DisplayName != nil && display != "" {
		if code := h.checkSubName(ctx, display); code != "" {
			return nil, huma.Error422UnprocessableEntity("bad_name", &huma.ErrorDetail{Location: "body.display_name", Message: code})
		}
	}
	row, err = h.d.Store.Q.UpdateInbound(ctx, db.UpdateInboundParams{Port: port, Enabled: enabled, Config: config, DisplayName: display, UpdatedAt: h.d.Now().Unix(), ID: in.ID})
	if err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "inbound.update", "inbound", row.Name, map[string]any{"config_changed": b.Config != nil || b.Dest != nil})
	return &inboundOutput{Body: h.viewInbound(row)}, nil
}

// detailCode pulls the first error code out of a huma error built by checkConfig.
func detailCode(err error) string {
	var m *huma.ErrorModel
	if errors.As(err, &m) && len(m.Errors) > 0 {
		return m.Errors[0].Message
	}
	return "invalid_config"
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
