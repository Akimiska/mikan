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
)

type SettingsView struct {
	Brand        string      `json:"brand"`
	SupportURL   string      `json:"support_url"`
	PublicHost   string      `json:"public_host"`
	Domain       string      `json:"domain"`
	PanelPort    int         `json:"panel_port"`
	QuietHourUTC int         `json:"quiet_hour_utc" doc:"Час (UTC), когда пополняется пул слотов: переподключение QUIC-клиентов"`
	AdminURL     string      `json:"admin_url"`
	SubBaseURL   string      `json:"sub_base_url"`
	Certificate  acme.Status `json:"certificate"`
}

type settingsOutput struct{ Body SettingsView }

type patchSettingsInput struct {
	Body struct {
		Brand        *string `json:"brand,omitempty" maxLength:"40"`
		SupportURL   *string `json:"support_url,omitempty" maxLength:"200" doc:"https://… или tg://…"`
		PublicHost   *string `json:"public_host,omitempty" maxLength:"253"`
		Domain       *string `json:"domain,omitempty" maxLength:"253"`
		QuietHourUTC *int    `json:"quiet_hour_utc,omitempty" minimum:"0" maximum:"23"`
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
	if err != nil {
		return v, err
	}
	if v.PanelPort, _, err = settings.Get[int](ctx, h.d.Settings, settings.KeyPanelPort); err != nil {
		return v, err
	}
	if v.QuietHourUTC, _, err = settings.Get[int](ctx, h.d.Settings, "quiet_hour_utc"); err != nil {
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
		details = append(details, &huma.ErrorDetail{Location: "body.public_host", Message: "Укажите IP-адрес или имя сервера"})
	}
	if b.Domain != nil && !validHost(*b.Domain) {
		details = append(details, &huma.ErrorDetail{Location: "body.domain", Message: "Домен вида vpn.example.com или пусто"})
	}
	if b.SupportURL != nil && *b.SupportURL != "" && !strings.HasPrefix(*b.SupportURL, "https://") && !strings.HasPrefix(*b.SupportURL, "tg://") {
		details = append(details, &huma.ErrorDetail{Location: "body.support_url", Message: "Ссылка должна начинаться с https:// или tg://"})
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
	for key, v := range map[string]*string{"brand": b.Brand, "support_url": b.SupportURL, settings.KeyPublicHost: b.PublicHost, settings.KeyDomain: b.Domain} {
		if err := set(key, v); err != nil {
			return nil, err
		}
	}
	if b.QuietHourUTC != nil {
		if err := settings.Set(ctx, h.d.Settings, "quiet_hour_utc", *b.QuietHourUTC); err != nil {
			return nil, err
		}
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.update", "", "", nil)
	v, err := h.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &settingsOutput{Body: v}, nil
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
