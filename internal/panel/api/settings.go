package api

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/acme"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/subs"
)

type SettingsView struct {
	Brand        string `json:"brand"`
	SupportURL   string `json:"support_url"`
	PublicHost   string `json:"public_host"`
	Domain       string `json:"domain"`
	PanelPort    int    `json:"panel_port"`
	QuietHourUTC int    `json:"quiet_hour_utc" doc:"Час (UTC), когда пополняется пул слотов: переподключение QUIC-клиентов"`
	AdminURL     string `json:"admin_url"`
	SubBaseURL   string `json:"sub_base_url"`
	SubGroupMain string `json:"sub_group_main" doc:"Главная группа в Clash-приложениях"`
	SubGroupAuto string `json:"sub_group_auto" doc:"Группа автовыбора самого быстрого подключения"`
	SubRouting   string `json:"sub_routing" enum:"ru_direct,all" doc:"Маршруты в Clash-приложениях: ru_direct — российские сайты и IP напрямую по геобазам mihomo, all — всё через VPN"`
	AutoPort     bool   `json:"auto_port" doc:"Переносить подключение на другой порт, если клиенты перестали до него доходить"`
	AutoSNI      bool   `json:"auto_sni" doc:"Менять сайт маскировки REALITY, если он перестал подходить"`
	// Devices: see domain.Devices.
	DeviceBinding bool        `json:"device_binding" doc:"Привязывать подписку к устройствам: у каждого устройства свои ключи"`
	RequireHWID   bool        `json:"device_require_hwid" doc:"Не выдавать подписку приложениям без ID устройства (иначе они вместе занимают одно место)"`
	Certificate   acme.Status `json:"certificate"`
}

type settingsOutput struct{ Body SettingsView }

type patchSettingsInput struct {
	Body struct {
		Brand         *string `json:"brand,omitempty" maxLength:"40"`
		SupportURL    *string `json:"support_url,omitempty" maxLength:"200" doc:"https://… или tg://…"`
		PublicHost    *string `json:"public_host,omitempty" maxLength:"253"`
		Domain        *string `json:"domain,omitempty" maxLength:"253"`
		QuietHourUTC  *int    `json:"quiet_hour_utc,omitempty" minimum:"0" maximum:"23"`
		SubGroupMain  *string `json:"sub_group_main,omitempty" maxLength:"200"`
		SubGroupAuto  *string `json:"sub_group_auto,omitempty" maxLength:"200"`
		SubRouting    *string `json:"sub_routing,omitempty" enum:"ru_direct,all"`
		AutoPort      *bool   `json:"auto_port,omitempty"`
		AutoSNI       *bool   `json:"auto_sni,omitempty"`
		DeviceBinding *bool   `json:"device_binding,omitempty"`
		RequireHWID   *bool   `json:"device_require_hwid,omitempty"`
	}
}

type resetPathOutput struct {
	Body struct {
		AdminURL string `json:"admin_url"`
	}
}

func (h *handlers) registerSettings() {
	huma.Register(h.api, huma.Operation{OperationID: "get-settings", Method: http.MethodGet, Path: "/api/v1/settings", Summary: "Настройки", Tags: []string{"settings"}}, h.getSettings)
	huma.Register(h.api, huma.Operation{OperationID: "update-settings", Method: http.MethodPatch, Path: "/api/v1/settings", Summary: "Изменить настройки", Tags: []string{"settings"}}, h.updateSettings)
	huma.Register(h.api, huma.Operation{OperationID: "reset-admin-path", Method: http.MethodPost, Path: "/api/v1/settings/reset-admin-path", Summary: "Выдать новую секретную ссылку на панель", Tags: []string{"settings"}}, h.resetAdminPath)
	huma.Register(h.api, huma.Operation{OperationID: "renew-certificate", Method: http.MethodPost, Path: "/api/v1/settings/certificate/renew", Summary: "Запросить сертификат Let's Encrypt сейчас", Tags: []string{"settings"}, DefaultStatus: http.StatusAccepted}, h.renewCertificate)
}

func (h *handlers) renewCertificate(ctx context.Context, _ *struct{}) (*struct{}, error) {
	if h.d.RenewCert == nil {
		return nil, huma.Error409Conflict("acme_disabled")
	}
	h.d.RenewCert()
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.renew_certificate", "", "", nil)
	return nil, nil
}

func (h *handlers) readSettings(ctx context.Context) (SettingsView, error) {
	var v SettingsView
	var err error
	get := func(key string, dst *string) {
		if err == nil {
			*dst, _, err = settings.Get[string](ctx, h.d.Settings, key)
		}
	}
	get("brand", &v.Brand)
	get("support_url", &v.SupportURL)
	get(settings.KeyPublicHost, &v.PublicHost)
	get(settings.KeyDomain, &v.Domain)
	get(settings.KeyGroupMain, &v.SubGroupMain)
	get(settings.KeyGroupAuto, &v.SubGroupAuto)
	get(settings.KeyRouting, &v.SubRouting)
	v.SubRouting = string(subs.ParseRouting(v.SubRouting))
	if v.SubGroupMain == "" {
		v.SubGroupMain = subs.DefaultMainGroup
	}
	if v.SubGroupAuto == "" {
		v.SubGroupAuto = subs.DefaultAutoGroup
	}
	if err != nil {
		return v, err
	}
	if v.PanelPort, _, err = settings.Get[int](ctx, h.d.Settings, settings.KeyPanelPort); err != nil {
		return v, err
	}
	if v.QuietHourUTC, _, err = settings.Get[int](ctx, h.d.Settings, "quiet_hour_utc"); err != nil {
		return v, err
	}
	if v.AutoPort, err = h.d.Settings.Bool(ctx, settings.KeyAutoPort, true); err != nil {
		return v, err
	}
	if v.AutoSNI, err = h.d.Settings.Bool(ctx, settings.KeyAutoSNI, true); err != nil {
		return v, err
	}
	if v.DeviceBinding, err = h.d.Settings.Bool(ctx, settings.KeyDeviceBinding, true); err != nil {
		return v, err
	}
	if v.RequireHWID, err = h.d.Settings.Bool(ctx, settings.KeyRequireHWID, false); err != nil {
		return v, err
	}
	if v.Brand == "" {
		v.Brand = "VPN"
	}
	paths, err := h.d.Settings.Paths(ctx)
	if err != nil {
		return v, err
	}
	host := v.Domain
	if host == "" {
		host = v.PublicHost
	}
	if host != "" {
		base := "https://" + net.JoinHostPort(host, strconv.Itoa(v.PanelPort)) + "/"
		v.AdminURL = base + paths.Admin + "/"
		v.SubBaseURL = base + paths.Sub + "/"
	}
	v.Certificate = acme.Status{Kind: "self-signed"}
	if h.d.Cert != nil {
		v.Certificate = h.d.Cert()
	}
	return v, nil
}

func (h *handlers) getSettings(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
	v, err := h.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &settingsOutput{Body: v}, nil
}

func validHost(s string) bool {
	if s == "" {
		return true
	}
	if net.ParseIP(s) != nil {
		return true
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return strings.Contains(s, ".")
}

func (h *handlers) updateSettings(ctx context.Context, in *patchSettingsInput) (*settingsOutput, error) {
	b := in.Body
	var details []error
	if b.PublicHost != nil && (*b.PublicHost == "" || !validHost(*b.PublicHost)) {
		details = append(details, &huma.ErrorDetail{Location: "body.public_host", Message: "public_host_invalid"})
	}
	if b.Domain != nil && !validHost(*b.Domain) {
		details = append(details, &huma.ErrorDetail{Location: "body.domain", Message: "domain_invalid"})
	}
	if b.SupportURL != nil && *b.SupportURL != "" && !strings.HasPrefix(*b.SupportURL, "https://") && !strings.HasPrefix(*b.SupportURL, "tg://") {
		details = append(details, &huma.ErrorDetail{Location: "body.support_url", Message: "support_url_invalid"})
	}
	if b.SubGroupMain != nil || b.SubGroupAuto != nil {
		cur, err := h.groups(ctx)
		if err != nil {
			return nil, err
		}
		next := cur
		if b.SubGroupMain != nil {
			next.Main = strings.TrimSpace(*b.SubGroupMain)
		}
		if b.SubGroupAuto != nil {
			next.Auto = strings.TrimSpace(*b.SubGroupAuto)
		}
		details = append(details, h.checkGroups(ctx, next)...)
	}
	if len(details) > 0 {
		return nil, huma.Error422UnprocessableEntity("validation", details...)
	}
	set := func(key string, v *string) error {
		if v == nil {
			return nil
		}
		return settings.Set(ctx, h.d.Settings, key, strings.TrimSpace(*v))
	}
	for key, v := range map[string]*string{"brand": b.Brand, "support_url": b.SupportURL, settings.KeyPublicHost: b.PublicHost, settings.KeyDomain: b.Domain,
		settings.KeyGroupMain: b.SubGroupMain, settings.KeyGroupAuto: b.SubGroupAuto, settings.KeyRouting: b.SubRouting} {
		if err := set(key, v); err != nil {
			return nil, err
		}
	}
	if b.QuietHourUTC != nil {
		if err := settings.Set(ctx, h.d.Settings, "quiet_hour_utc", *b.QuietHourUTC); err != nil {
			return nil, err
		}
	}
	for key, v := range map[string]*bool{settings.KeyAutoPort: b.AutoPort, settings.KeyAutoSNI: b.AutoSNI,
		settings.KeyDeviceBinding: b.DeviceBinding, settings.KeyRequireHWID: b.RequireHWID} {
		if v != nil {
			if err := settings.Set(ctx, h.d.Settings, key, *v); err != nil {
				return nil, err
			}
		}
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.update", "", "", nil)
	v, err := h.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &settingsOutput{Body: v}, nil
}

// groups returns the subscription group names with defaults applied.
func (h *handlers) groups(ctx context.Context) (subs.Groups, error) {
	var g subs.Groups
	var err error
	if g.Main, err = h.d.Settings.String(ctx, settings.KeyGroupMain); err != nil {
		return g, err
	}
	if g.Auto, err = h.d.Settings.String(ctx, settings.KeyGroupAuto); err != nil {
		return g, err
	}
	if g.Main == "" {
		g.Main = subs.DefaultMainGroup
	}
	if g.Auto == "" {
		g.Auto = subs.DefaultAutoGroup
	}
	return g, nil
}

// checkGroups: a profile with a group named like a proxy, a built-in policy or the
// other group does not load in any Clash app.
func (h *handlers) checkGroups(ctx context.Context, g subs.Groups) []error {
	var out []error
	bad := func(field, code string, value any) {
		out = append(out, &huma.ErrorDetail{Location: "body." + field, Message: code, Value: value})
	}
	if err := subs.ValidName(g.Main); err != nil {
		bad("sub_group_main", err.Error(), nil)
	}
	if err := subs.ValidName(g.Auto); err != nil {
		bad("sub_group_auto", err.Error(), nil)
	}
	if strings.EqualFold(g.Auto, subs.AliasGroup) {
		bad("sub_group_auto", "group_alias_taken", subs.AliasGroup)
	}
	if strings.EqualFold(g.Main, g.Auto) {
		bad("sub_group_auto", "groups_same", nil)
	}
	if inbounds, err := h.d.Store.Q.ListInbounds(ctx); err == nil {
		for _, in := range inbounds {
			name := subs.ProxyName(in)
			if strings.EqualFold(name, g.Main) {
				bad("sub_group_main", "group_is_proxy", in.Name)
			}
			if strings.EqualFold(name, g.Auto) {
				bad("sub_group_auto", "group_is_proxy", in.Name)
			}
		}
	}
	return out
}

// checkSubName validates an inbound's name in the subscription and returns an error
// code, "" when the name is fine.
func (h *handlers) checkSubName(ctx context.Context, name string) string {
	if err := subs.ValidName(name); err != nil {
		return err.Error()
	}
	g, err := h.groups(ctx)
	if err != nil {
		return ""
	}
	for _, x := range []string{g.Main, g.Auto, subs.AliasGroup} {
		if strings.EqualFold(name, x) {
			return "name_is_group"
		}
	}
	return ""
}

func (h *handlers) resetAdminPath(ctx context.Context, _ *struct{}) (*resetPathOutput, error) {
	if err := settings.Set(ctx, h.d.Settings, settings.KeyAdminPath, secure.Token(24)); err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.reset_admin_path", "", "", nil)
	v, err := h.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := &resetPathOutput{}
	out.Body.AdminURL = v.AdminURL
	return out, nil
}
