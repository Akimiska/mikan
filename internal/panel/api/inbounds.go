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
	"mikan/internal/panel/secure"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/subs"
	"mikan/internal/proto"
)

type InboundView struct {
	ID          int64    `json:"id"`
	NodeID      int64    `json:"node_id"`
	Name        string   `json:"name"`
	Preset      string   `json:"preset"`
	Title       string   `json:"title"`
	Type        string   `json:"type" doc:"Тип листенера mihomo"`
	Network     string   `json:"network"`
	Port        string   `json:"port"`
	Listen      string   `json:"listen" doc:"Адрес, на котором нода слушает: пусто — все адреса, 127.0.0.1 — только сам сервер (за nginx или HAProxy)"`
	Enabled     bool     `json:"enabled"`
	DisplayName string   `json:"display_name" doc:"Своё имя в подписке; пусто — имя по умолчанию"`
	SubName     string   `json:"sub_name" doc:"Имя, которое увидит клиент"`
	Config      string   `json:"config" doc:"Шаблон листенера (YAML)"`
	Dest        string   `json:"dest,omitempty" doc:"Сайт для маскировки REALITY"`
	ServerNames []string `json:"server_names,omitempty"`
	// Fingerprint is the inbound's own uTLS profile, "" for the panel's default; absent when
	// its clients do not dial through uTLS (QUIC protocols, shared keys).
	Fingerprint *string        `json:"fingerprint,omitempty" doc:"Отпечаток TLS (uTLS) у клиентов; пусто — общий из настроек"`
	Obfs        *string        `json:"obfs,omitempty" doc:"Hysteria2: salamander, gecko или пусто (без обфускации); у других типов поля нет"`
	Status      string         `json:"status" enum:"ok,error,unknown"`
	Error       string         `json:"error,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Apps        []string       `json:"apps" doc:"Приложения, которым подключение попадает в подписку: mihomo, xray, singbox, stash, other"`
	Shared      bool           `json:"shared,omitempty" doc:"Один ключ на всех: учёт, лимиты и отключение по пользователям не работают"`
	AutoPort    bool           `json:"auto_port" doc:"Панель сама переносит подключение на другой порт, если его блокируют (и включено в настройках)"`
	AutoSNI     bool           `json:"auto_sni" doc:"Панель сама меняет сайт маскировки REALITY, если он перестал подходить (и включено в настройках)"`
	Auto        AutoView       `json:"auto"`
	Outbound    string         `json:"outbound" enum:"direct,warp,node" doc:"Выход в интернет: напрямую с сервера, через WARP ноды или через другую ноду (каскад)"`
	ExitNodeID  *int64         `json:"exit_node_id,omitempty" doc:"Нода, через которую выходит трафик, если outbound=node"`
	PoolID      *int64         `json:"pool_id,omitempty" doc:"Пул трафика, в который считается подключение; нет — основной трафик"`
	Client      ClientEndpoint `json:"client" doc:"Куда подключаются клиенты, если не к ноде напрямую (mikan.client в шаблоне)"`
	ClientSNI   bool           `json:"client_sni" doc:"Можно ли задать клиентам свой SNI: у REALITY имя задаёт сайт маскировки"`
}

// ClientEndpoint is where clients connect when a TCP proxy or a CDN stands in front of
// the node. Empty values (port 0) keep the node's address, the inbound's port and SNI.
type ClientEndpoint struct {
	Server string `json:"server" maxLength:"253" doc:"Адрес для клиентов; пусто — адрес ноды"`
	Port   int    `json:"port" minimum:"0" maximum:"65535" doc:"Порт для клиентов; 0 — порт подключения"`
	SNI    string `json:"sni" maxLength:"253" doc:"SNI для клиентов; пусто — как обычно"`
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
		Preset string `json:"preset" enum:"vless_reality_xhttp,hysteria2,hysteria2_gecko,tuic_v5,vless_reality_vision,vless_reality_grpc,trojan_reality,anytls,vless_reality_xhttp_pq,trusttunnel,shadowquic,mieru,shadowsocks_2022,sudoku,snell,custom"`
		NodeID int64  `json:"node_id,omitempty" minimum:"1" doc:"Нода; по умолчанию — своя нода панели"`
		Port   string `json:"port,omitempty" pattern:"^[0-9]{1,5}(-[0-9]{1,5})?$"`
		Dest   string `json:"dest,omitempty" maxLength:"255" doc:"host:port для REALITY"`
		Config string `json:"config,omitempty" maxLength:"65536" doc:"Шаблон листенера (YAML) для preset=custom"`
	}
}

type patchInboundInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Port        *string         `json:"port,omitempty" pattern:"^[0-9]{1,5}(-[0-9]{1,5})?$"`
		Enabled     *bool           `json:"enabled,omitempty"`
		Dest        *string         `json:"dest,omitempty" maxLength:"255"`
		ServerName  *string         `json:"server_name,omitempty" maxLength:"253" doc:"SNI для клиентов, если dest — IP (цель из подбора соседей)"`
		Obfs        *string         `json:"obfs,omitempty" enum:"salamander,gecko" doc:"Обфускация Hysteria2. Gecko понимают только приложения на ядре mihomo 1.19.26+: остальные это подключение не получат"`
		Fingerprint *string         `json:"fingerprint,omitempty" maxLength:"32" doc:"Отпечаток TLS у клиентов: из списка (chrome, firefox, safari, ios, android, edge, 360, qq, random, randomized) или своё — латиница, цифры, _; пусто — общий из настроек"`
		DisplayName *string         `json:"display_name,omitempty" maxLength:"200" doc:"Можно с эмодзи: «🇳🇱 Нидерланды». Пусто — имя по умолчанию"`
		Config      *string         `json:"config,omitempty" maxLength:"65536" doc:"Шаблон листенера (YAML)"`
		Listen      *string         `json:"listen,omitempty" maxLength:"64" doc:"Адрес, на котором нода слушает: пусто — все адреса, иначе один IP (127.0.0.1 — за nginx или HAProxy на том же сервере). Свой адрес выключает перенос порта"`
		Client      *ClientEndpoint `json:"client,omitempty" doc:"Куда подключаются клиенты: адрес, порт и SNI прокси перед нодой; заменяет все три"`
		AutoPort    *bool           `json:"auto_port,omitempty" doc:"Нельзя включить, пока у подключения свой адрес (listen)"`
		AutoSNI     *bool           `json:"auto_sni,omitempty"`
		Outbound    *string         `json:"outbound,omitempty" enum:"direct,warp,node" doc:"Выход в интернет: напрямую, через WARP ноды или через другую ноду"`
		ExitNodeID  *int64          `json:"exit_node_id,omitempty" minimum:"1" doc:"Для outbound=node: через какую ноду"`
		PoolID      *int64          `json:"pool_id,omitempty" minimum:"0" doc:"Пул трафика; 0 — основной трафик"`
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
		AutoPort: in.AutoPort != 0, AutoSNI: in.AutoSni != 0, Outbound: in.Outbound, Listen: in.Listen}
	if in.PoolID.Valid {
		id := in.PoolID.Int64
		v.PoolID = &id
	}
	if in.ExitNodeID.Valid {
		id := in.ExitNodeID.Int64
		v.Outbound, v.ExitNodeID = "node", &id
	}
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
		c := t.Ext().Client
		v.Client, v.ClientSNI = ClientEndpoint{Server: c.Server, Port: c.Port, SNI: c.SNI}, proto.ClientSNI(t)
		if proto.UsesFingerprint(t) {
			fp := c.Fingerprint
			v.Fingerprint = &fp
		}
		if t.Type() == "hysteria2" {
			obfs := proto.Obfs(t)
			v.Obfs = &obfs
		}
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
		// Checked here, not in proto.Validate: nodes keep applying templates saved before.
		if fp := t.Ext().Client.Fingerprint; err == nil && fp != "" && !proto.ValidFingerprint(fp) {
			err = &proto.Error{Code: "config_fingerprint", Field: "mikan.client.fingerprint", Detail: fp}
		}
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
	base := info.Name
	if info.ID == presets.Custom {
		base = t.Type()
	}
	var row db.Inbound
	err = h.d.Store.Tx(ctx, func(q *db.Queries) error {
		if err := domain.CheckPort(ctx, q, node, port, t.Network(), domain.PortHolder{}); err != nil {
			return portError(err)
		}
		all, err := q.ListInbounds(ctx)
		if err != nil {
			return err
		}
		now := h.d.Now().Unix()
		row, err = q.CreateInbound(ctx, db.CreateInboundParams{NodeID: node.ID, Name: domain.FreeName(domain.NodeInbounds(all, node.ID), base), Preset: info.ID, Port: port,
			Config: config, CreatedAt: now, UpdatedAt: now})
		return err
	})
	if err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "inbound.create", "inbound", row.Name, map[string]any{"preset": info.ID, "type": t.Type(), "port": port})
	return &inboundOutput{Body: h.viewInbound(row, nil)}, nil
}

// portError is how the API reports a port something else holds; other errors pass
// through.
func portError(err error) error {
	var busy *domain.PortInUseError
	if !errors.As(err, &busy) {
		return err
	}
	switch busy.Kind {
	case domain.PortRelay:
		return huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "port_in_use", Value: "relay"})
	case domain.PortSub:
		return huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "port_sub"})
	case domain.PortPanel:
		return huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "port_panel"})
	case domain.PortNodeAPI:
		return huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "port_node_api"})
	}
	return huma.Error409Conflict("port_in_use", &huma.ErrorDetail{Location: "body.port", Message: "port_in_use", Value: busy.Name})
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
	// Every field is checked before anything is written, and everything is written in
	// one transaction: a request refused on any field leaves the inbound as it was.
	listen := row.Listen
	if b.Listen != nil {
		if listen, err = domain.ParseListen(*b.Listen); err != nil {
			return nil, huma.Error422UnprocessableEntity("bad_listen", &huma.ErrorDetail{Location: "body.listen", Message: "bad_listen"})
		}
	}
	autoPort, autoSNI := row.AutoPort, row.AutoSni
	if b.AutoPort != nil {
		autoPort = flag(*b.AutoPort)
	}
	if b.AutoSNI != nil {
		autoSNI = flag(*b.AutoSNI)
	}
	if domain.ListenPinsPort(listen) {
		if b.AutoPort != nil && *b.AutoPort {
			return nil, huma.Error422UnprocessableEntity("auto_port_listen", &huma.ErrorDetail{Location: "body.auto_port", Message: "auto_port_listen"})
		}
		autoPort = 0
	}
	moved := listen != row.Listen
	nodeSide := moved || autoPort != row.AutoPort || autoSNI != row.AutoSni
	// The traffic pool changes only how the node counts, the way out is the node's
	// business: clients get nothing new from either.
	pool := row.PoolID
	if b.PoolID != nil {
		pool = sql.NullInt64{Int64: *b.PoolID, Valid: *b.PoolID != 0}
	}
	outbound, exit := row.Outbound, row.ExitNodeID
	if b.Outbound != nil {
		outbound, exit = *b.Outbound, sql.NullInt64{}
		if outbound == "node" {
			if b.ExitNodeID == nil {
				return nil, cascadeError(domain.ErrNotFound, "exit_node_id")
			}
			outbound, exit = "direct", sql.NullInt64{Int64: *b.ExitNodeID, Valid: true}
		}
	}
	// What clients see bumps updated_at. Without it the block detector keeps trusting the
	// profiles clients have.
	client := b.Port != nil || b.Enabled != nil || b.Config != nil || b.Dest != nil || b.Fingerprint != nil || b.Obfs != nil || b.DisplayName != nil || b.Client != nil
	port, enabled, config, display := row.Port, row.Enabled, row.Config, row.DisplayName
	var node db.Node
	var network string
	if client {
		if b.Port != nil {
			if !domain.ValidPort(*b.Port) {
				return nil, huma.Error422UnprocessableEntity("bad_port", &huma.ErrorDetail{Location: "body.port", Message: "bad_port"})
			}
			port = *b.Port
		}
		if b.Enabled != nil {
			enabled = flag(*b.Enabled)
		}
		if b.Config != nil {
			config = *b.Config
		}
		if b.Dest != nil {
			sni := ""
			if b.ServerName != nil {
				sni = strings.TrimSpace(*b.ServerName)
			}
			if config, err = editConfig(config, "body.dest", "bad_dest", func(t proto.Template) error {
				return presets.SetDest(t, strings.TrimSpace(*b.Dest), sni)
			}); err != nil {
				return nil, err
			}
		}
		if b.Fingerprint != nil {
			if config, err = editConfig(config, "body.fingerprint", "bad_fingerprint", func(t proto.Template) error {
				if !proto.UsesFingerprint(t) {
					return &proto.Error{Code: "fingerprint_no_tls", Field: "mikan.client.fingerprint"}
				}
				return proto.SetFingerprint(t, strings.TrimSpace(*b.Fingerprint))
			}); err != nil {
				return nil, err
			}
		}
		if b.Obfs != nil {
			if config, err = editConfig(config, "body.obfs", "bad_obfs", func(t proto.Template) error {
				return proto.SetObfs(t, *b.Obfs, secure.Token(24))
			}); err != nil {
				return nil, err
			}
		}
		if c := b.Client; c != nil {
			if config, err = editConfig(config, "body.client", "bad_client", func(t proto.Template) error {
				return proto.SetClientEndpoint(t, strings.TrimSpace(c.Server), c.Port, strings.TrimSpace(c.SNI))
			}); err != nil {
				return nil, err
			}
		}
		if b.DisplayName != nil {
			display = strings.TrimSpace(*b.DisplayName)
			if display != "" {
				if code := h.checkSubName(ctx, display); code != "" {
					return nil, huma.Error422UnprocessableEntity("bad_name", &huma.ErrorDetail{Location: "body.display_name", Message: code})
				}
			}
		}
		if node, err = h.nodeOf(ctx, row.NodeID); err != nil {
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
		network = t.Network()
	}
	err = h.d.Store.Tx(ctx, func(q *db.Queries) error {
		// What the rest of the panel holds is checked on the transaction that writes.
		if client && enabled != 0 {
			if err := domain.CheckPort(ctx, q, node, port, network, domain.InboundHolder(row)); err != nil {
				return portError(err)
			}
		}
		if b.DisplayName != nil {
			all, err := q.ListInbounds(ctx)
			if err != nil {
				return err
			}
			// Names are per node: other nodes' links get their own flag prefix.
			next := row
			next.DisplayName = display
			for _, e := range domain.NodeInbounds(all, row.NodeID) {
				if e.ID != row.ID && strings.EqualFold(subs.ProxyName(e), subs.ProxyName(next)) {
					return huma.Error409Conflict("name_in_use", &huma.ErrorDetail{Location: "body.display_name", Message: "name_in_use", Value: e.Name})
				}
			}
		}
		if b.PoolID != nil && pool.Valid {
			if _, err := q.GetTrafficPool(ctx, pool.Int64); errors.Is(err, sql.ErrNoRows) {
				return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.pool_id", Message: "pool_not_found"})
			} else if err != nil {
				return err
			}
		}
		// The exit's chain is checked, and its relay made, before the inbound changes.
		if b.Outbound != nil && exit.Valid {
			if err := h.useExit(ctx, q, row.NodeID, exit.Int64); err != nil {
				return cascadeError(err, "exit_node_id")
			}
		}
		if b.PoolID != nil {
			if err := q.SetInboundPool(ctx, db.SetInboundPoolParams{PoolID: pool, ID: row.ID}); err != nil {
				return err
			}
		}
		if b.Outbound != nil {
			if err := q.SetInboundExit(ctx, db.SetInboundExitParams{ExitNodeID: exit, Outbound: outbound, ID: row.ID}); err != nil {
				return err
			}
		}
		row.PoolID, row.Outbound, row.ExitNodeID = pool, outbound, exit
		if client {
			var err error
			if row, err = q.UpdateInbound(ctx, db.UpdateInboundParams{Port: port, Enabled: enabled, Config: config, DisplayName: display, UpdatedAt: h.d.Now().Unix(), ID: row.ID}); err != nil {
				return err
			}
		}
		return saveNodeSide(ctx, q, &row, listen, autoPort, autoSNI)
	})
	if err != nil {
		return nil, err
	}
	admin := sessionOf(ctx).AdminID
	if b.PoolID != nil {
		h.d.Changes.PoliciesChanged()
		h.audit(ctx, admin, "inbound.pool", "inbound", row.Name, map[string]any{"pool_id": pool.Int64})
	}
	if b.Outbound != nil {
		h.audit(ctx, admin, "inbound.outbound", "inbound", row.Name, map[string]any{"outbound": *b.Outbound, "exit_node_id": exit.Int64})
	}
	switch {
	case client:
		h.audit(ctx, admin, "inbound.update", "inbound", row.Name, map[string]any{"config_changed": b.Config != nil || b.Dest != nil || b.Fingerprint != nil || b.Obfs != nil || b.Client != nil,
			"listen": row.Listen, "auto_port": row.AutoPort != 0, "auto_sni": row.AutoSni != 0})
	case nodeSide:
		h.audit(ctx, admin, "inbound.auto", "inbound", row.Name, map[string]any{"listen": row.Listen, "auto_port": row.AutoPort != 0, "auto_sni": row.AutoSni != 0})
	}
	if client || moved || b.PoolID != nil || b.Outbound != nil {
		h.d.Changes.SlotsChanged()
	}
	last, err := h.lastAuto(ctx)
	if err != nil {
		return nil, err
	}
	return &inboundOutput{Body: h.viewInbound(row, last)}, nil
}

// saveNodeSide writes what only the node uses: the listen address and the
// automatic-move switches. Clients get nothing new from them.
func saveNodeSide(ctx context.Context, q *db.Queries, row *db.Inbound, listen string, autoPort, autoSNI int64) error {
	if listen != row.Listen {
		if err := q.SetInboundListen(ctx, db.SetInboundListenParams{Listen: listen, ID: row.ID}); err != nil {
			return err
		}
		row.Listen = listen
	}
	if autoPort != row.AutoPort || autoSNI != row.AutoSni {
		if err := q.SetInboundAuto(ctx, db.SetInboundAutoParams{AutoPort: autoPort, AutoSni: autoSNI, ID: row.ID}); err != nil {
			return err
		}
		row.AutoPort, row.AutoSni = autoPort, autoSNI
	}
	return nil
}

// editConfig applies a form field to the template. A template the field does not fit is
// reported at location with the template's own error code; the client endpoint names its
// part (body.client.sni).
func editConfig(config, location, code string, edit func(proto.Template) error) (string, error) {
	t, err := proto.Parse(config)
	if err == nil {
		err = edit(t)
	}
	var pe *proto.Error
	if errors.As(err, &pe) {
		if part, ok := strings.CutPrefix(pe.Field, "mikan.client."); ok && location == "body.client" {
			location += "." + part
		}
		return "", huma.Error422UnprocessableEntity(code, &huma.ErrorDetail{Location: location, Message: pe.Code})
	}
	if err != nil {
		return "", err
	}
	return proto.Marshal(t), nil
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
