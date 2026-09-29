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
	NodeID      int64     `json:"node_id"`
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
	Apps        []string  `json:"apps" doc:"Приложения, которым подключение попадает в подписку: mihomo, xray, singbox, stash, other"`
	Shared      bool      `json:"shared,omitempty" doc:"Один ключ на всех: учёт, лимиты и отключение по пользователям не работают"`
	AutoPort    bool      `json:"auto_port" doc:"Панель сама переносит подключение на другой порт, если его блокируют (и включено в настройках)"`
	AutoSNI     bool      `json:"auto_sni" doc:"Панель сама меняет сайт маскировки REALITY, если он перестал подходить (и включено в настройках)"`
	Auto        AutoView  `json:"auto"`
}

// AutoView is what the automatic moves see and last did for an inbound.
type AutoView struct {
	CutOff  bool       `json:"cut_off" doc:"Устройства, которые доходят до других подключений ноды, до этого не доходят"`
	Blocked int        `json:"blocked" doc:"Сколько таких устройств"`
	Reached int        `json:"reached" doc:"Сколько из проверяющих все подключения устройств до него дошли"`
	Since   *time.Time `json:"since,omitempty"`
	Stuck   string     `json:"stuck,omitempty" enum:"off,waiting,no_port,no_target,exhausted" doc:"Почему отрезанное подключение остаётся как есть"`
	// The REALITY target's last check, absent before the first one.
	TargetOK    *bool      `json:"target_ok,omitempty"`
	TargetError string     `json:"target_error,omitempty"`
	Last        *AutoEvent `json:"last,omitempty" doc:"Последняя автоматическая смена"`
}

type AutoEvent struct {
	Kind   string    `json:"kind" enum:"port,sni"`
	Old    string    `json:"old"`
	New    string    `json:"new"`
	Reason string    `json:"reason" enum:"blocked,target_down,still_blocked"`
	At     time.Time `json:"at"`
}

type inboundsOutput struct{ Body []InboundView }
type inboundOutput struct{ Body InboundView }

type createInboundInput struct {
	Body struct {
		Preset string `json:"preset" enum:"vless_reality_xhttp,hysteria2,tuic_v5,vless_reality_vision,vless_reality_grpc,trojan_reality,anytls,vless_reality_xhttp_pq,trusttunnel,shadowquic,mieru,shadowsocks_2022,sudoku,snell,custom"`
		NodeID int64  `json:"node_id,omitempty" minimum:"1" doc:"Нода; по умолчанию — своя нода панели"`
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
		AutoPort    *bool   `json:"auto_port,omitempty"`
		AutoSNI     *bool   `json:"auto_sni,omitempty"`
	}
}

type validateInboundInput struct {
	Body struct {
		NodeID int64  `json:"node_id,omitempty" minimum:"1"`
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

// lastAuto maps inbound ids to their latest automatic change.
func (h *handlers) lastAuto(ctx context.Context) (map[int64]db.InboundEvent, error) {
	rows, err := h.d.Store.Q.LastInboundEvents(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]db.InboundEvent, len(rows))
	for _, e := range rows {
		out[e.InboundID] = e
	}
	return out, nil
}

func (h *handlers) viewInbound(in db.Inbound, last map[int64]db.InboundEvent) InboundView {
	info, _ := presets.Get(in.Preset)
	v := InboundView{ID: in.ID, NodeID: in.NodeID, Name: in.Name, Preset: in.Preset, Title: info.Title, Port: in.Port, Enabled: in.Enabled != 0,
		DisplayName: in.DisplayName, SubName: subs.ProxyName(in), Config: in.Config, Status: "unknown", UpdatedAt: time.Unix(in.UpdatedAt, 0).UTC(),
		AutoPort: in.AutoPort != 0, AutoSNI: in.AutoSni != 0}
	if e, ok := last[in.ID]; ok {
		v.Auto.Last = &AutoEvent{Kind: e.Kind, Old: e.OldValue, New: e.NewValue, Reason: e.Reason, At: time.Unix(e.CreatedAt, 0).UTC()}
	}
	if h.d.Tuner != nil {
		if s, ok := h.d.Tuner.Status(in.ID); ok {
			v.Auto.CutOff, v.Auto.Blocked, v.Auto.Reached, v.Auto.Stuck = s.CutOff, s.Blocked, s.Reached, s.Stuck
			if s.CutOff {
				since := s.Since.UTC()
				v.Auto.Since = &since
			}
			if !s.TargetAt.IsZero() {
				ok := s.TargetOK
				v.Auto.TargetOK, v.Auto.TargetError = &ok, s.TargetError
			}
		}
	}
	v.Apps = []string{}
	if t, err := proto.Parse(in.Config); err == nil {
		v.Type, v.Network = t.Type(), t.Network()
		v.Shared = proto.Shared(t.Type())
		for _, f := range subs.AppsFor(proto.NeedsOf(t)) {
			v.Apps = append(v.Apps, string(f))
		}
		v.Dest, v.ServerNames = presets.Dest(t)
		if in.Preset == presets.Custom {
			v.Title = t.Type()
		}
	}
	if h.d.Nodes != nil {
		hv, _ := h.d.Nodes.Health(in.NodeID)
		for _, l := range hv.Listeners {
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
	last, err := h.lastAuto(ctx)
	if err != nil {
		return nil, err
	}
	out := &inboundsOutput{Body: make([]InboundView, 0, len(rows))}
	for _, in := range rows {
		out.Body = append(out.Body, h.viewInbound(in, last))
	}
	return out, nil
}

// checkConfig parses and validates a template: mikan's rules first, then mihomo's own
// parser on the node, so a broken template never replaces a working listener.
func (h *handlers) checkConfig(ctx context.Context, node db.Node, config, port string) (proto.Template, error) {
	t, err := proto.Parse(config)
	if err == nil {
		var panelPort int
		// Only the panel's own node can use the panel as its REALITY target.
		if node.Address == "" {
			if panelPort, _, err = settings.Get[int](ctx, h.d.Settings, settings.KeyPanelPort); err != nil {
				return nil, err
			}
		}
		err = proto.Validate(t, proto.Options{SelfStealPort: panelPort})
		if err == nil && h.d.Nodes != nil {
			err = h.d.Nodes.Validate(ctx, node.ID, nodeapi.ValidateRequest{Inbound: nodeapi.Inbound{Name: "validate", Port: port, Config: t.JSON()}, SelfStealPort: panelPort})
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
	node, err := h.nodeOf(ctx, in.Body.NodeID)
	if err != nil {
		return nil, err
	}
	t, err := h.checkConfig(ctx, node, in.Body.Config, port)
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
	node, err := h.nodeOf(ctx, in.Body.NodeID)
	if err != nil {
		return nil, err
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
		if config, err = presets.NewConfig(info.ID, in.Body.Dest); err != nil {
			return nil, err
		}
	}
	t, err := h.checkConfig(ctx, node, config, port)
	if err != nil {
		return nil, err
	}
	all, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	existing := domain.NodeInbounds(all, node.ID)
	base := info.Name
	if info.ID == presets.Custom {
		base = t.Type()
	}
	if owner, busy := domain.PortOwner(existing, port, t.Network(), 0); busy {
		return nil, huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "port_in_use", Value: owner.Name})
	}
	name := domain.FreeName(existing, base)
	now := h.d.Now().Unix()
	row, err := h.d.Store.Q.CreateInbound(ctx, db.CreateInboundParams{NodeID: node.ID, Name: name, Preset: info.ID, Port: port, Config: config, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "inbound.create", "inbound", name, map[string]any{"preset": info.ID, "type": t.Type(), "port": port})
	return &inboundOutput{Body: h.viewInbound(row, nil)}, nil
}

func flag(on bool) int64 {
	if on {
		return 1
	}
	return 0
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
	autoPort, autoSNI := row.AutoPort, row.AutoSni
	if b.AutoPort != nil {
		autoPort = flag(*b.AutoPort)
	}
	if b.AutoSNI != nil {
		autoSNI = flag(*b.AutoSNI)
	}
	autoChanged := autoPort != row.AutoPort || autoSNI != row.AutoSni
	if b.Port == nil && b.Enabled == nil && b.Config == nil && b.Dest == nil && b.DisplayName == nil {
		// Only the automatic-move switches: clients get nothing new, so updated_at stays
		// and the block detector keeps trusting their profiles.
		if autoChanged {
			if err := h.d.Store.Q.SetInboundAuto(ctx, db.SetInboundAutoParams{AutoPort: autoPort, AutoSni: autoSNI, ID: row.ID}); err != nil {
				return nil, err
			}
			h.audit(ctx, sessionOf(ctx).AdminID, "inbound.auto", "inbound", row.Name, map[string]any{"auto_port": autoPort != 0, "auto_sni": autoSNI != 0})
		}
		row.AutoPort, row.AutoSni = autoPort, autoSNI
		last, err := h.lastAuto(ctx)
		if err != nil {
			return nil, err
		}
		return &inboundOutput{Body: h.viewInbound(row, last)}, nil
	}
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
	node, err := h.nodeOf(ctx, row.NodeID)
	if err != nil {
		return nil, err
	}
	t, err := h.checkConfig(ctx, node, config, port)
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
	all, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	// Ports and names are per node: other nodes' links get their own flag prefix.
	existing := domain.NodeInbounds(all, row.NodeID)
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
	err = h.d.Store.Tx(ctx, func(q *db.Queries) error {
		var err error
		if row, err = q.UpdateInbound(ctx, db.UpdateInboundParams{Port: port, Enabled: enabled, Config: config, DisplayName: display, UpdatedAt: h.d.Now().Unix(), ID: in.ID}); err != nil {
			return err
		}
		if autoChanged {
			if err := q.SetInboundAuto(ctx, db.SetInboundAutoParams{AutoPort: autoPort, AutoSni: autoSNI, ID: row.ID}); err != nil {
				return err
			}
			row.AutoPort, row.AutoSni = autoPort, autoSNI
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "inbound.update", "inbound", row.Name, map[string]any{"config_changed": b.Config != nil || b.Dest != nil,
		"auto_port": row.AutoPort != 0, "auto_sni": row.AutoSni != 0})
	last, err := h.lastAuto(ctx)
	if err != nil {
		return nil, err
	}
	return &inboundOutput{Body: h.viewInbound(row, last)}, nil
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

// nodeOf loads the node an inbound belongs to; 0 is the panel's own node.
func (h *handlers) nodeOf(ctx context.Context, id int64) (db.Node, error) {
	if id == 0 {
		id = 1
	}
	n, err := h.d.Store.Q.GetNode(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return n, huma.Error422UnprocessableEntity("unknown_node", &huma.ErrorDetail{Location: "body.node_id", Message: "unknown_node"})
	}
	return n, err
}
