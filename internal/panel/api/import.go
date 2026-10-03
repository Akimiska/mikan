package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/panelimport"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// Users from another panel (panelimport). Everything here is the session's: it reads the
// old panel with its admin's password and creates users here. The credentials are used
// for the request and kept nowhere.

type importSource struct {
	Kind     string `json:"kind" enum:"marzban,pasarguard,remnawave"`
	URL      string `json:"url" maxLength:"300" doc:"Адрес старой панели: https://panel.example.com; http:// — только для этого сервера или локальной сети"`
	Username string `json:"username,omitempty" maxLength:"200" doc:"Marzban, PasarGuard: логин администратора"`
	Password string `json:"password,omitempty" maxLength:"500"`
	Token    string `json:"token,omitempty" maxLength:"4096" doc:"Remnawave: API-токен; PasarGuard: API-ключ вместо логина"`
}

func (s importSource) source() panelimport.Source {
	return panelimport.Source{Kind: panelimport.Kind(s.Kind), URL: s.URL, Username: s.Username, Password: s.Password, Token: s.Token}
}

// host is the source's address for the audit log and the state: scheme and host, no path
// and no credentials.
func (s importSource) host() string {
	u, err := url.Parse(strings.TrimSpace(s.URL))
	if err != nil {
		return s.Kind
	}
	return s.Kind + " " + u.Scheme + "://" + u.Host
}

type importPreviewInput struct{ Body importSource }

type importPreviewOutput struct{ Body panelimport.Preview }

type importRunInput struct {
	Body struct {
		Kind     string `json:"kind" enum:"marzban,pasarguard,remnawave"`
		URL      string `json:"url" maxLength:"300"`
		Username string `json:"username,omitempty" maxLength:"200"`
		Password string `json:"password,omitempty" maxLength:"500"`
		Token    string `json:"token,omitempty" maxLength:"4096"`
		TariffID int64  `json:"tariff_id" minimum:"1" doc:"Тариф, на котором появятся пользователи; лимит, срок и устройства берутся из старой панели, сброс трафика, протоколы и пулы — из тарифа"`
	}
}

type importStateOutput struct{ Body panelimport.JobState }

type LegacyView struct {
	Path      string `json:"path" doc:"Путь старых ссылок подписки: sub у Marzban и PasarGuard, api/sub у Remnawave; пусто — выключено"`
	Kind      string `json:"kind" doc:"Чьи ссылки: marzban, pasarguard или remnawave; импорт ставит его сам"`
	SecretSet bool   `json:"secret_set" doc:"Секрет старой панели задан; сам он не возвращается"`
	Links     int64  `json:"links" doc:"Сколько старых ссылок и пользователей заведено"`
}

type legacyOutput struct{ Body LegacyView }

type patchLegacyInput struct {
	Body struct {
		Path   *string `json:"path,omitempty" maxLength:"64"`
		Kind   *string `json:"kind,omitempty" enum:"marzban,pasarguard,remnawave,"`
		Secret *string `json:"secret,omitempty" maxLength:"500" doc:"Секрет из таблицы jwt базы старой панели; пусто — убрать"`
	}
}

func (h *handlers) registerImport() {
	tags := []string{"settings"}
	huma.Register(h.api, huma.Operation{OperationID: "import-preview", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/import/preview", Summary: "Что перенесётся из другой панели", Tags: tags}, h.importPreview)
	huma.Register(h.api, huma.Operation{OperationID: "import-run", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/import", Summary: "Перенести пользователей из другой панели",
		Description: "Импорт идёт в фоне: ответ 202 сразу, ход и итог — в GET /api/v1/import/status.", Tags: tags, DefaultStatus: http.StatusAccepted}, h.importRun)
	huma.Register(h.api, huma.Operation{OperationID: "import-status", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodGet, Path: "/api/v1/import/status", Summary: "Ход импорта", Tags: tags}, h.importStatus)
	huma.Register(h.api, huma.Operation{OperationID: "get-legacy-links", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodGet, Path: "/api/v1/import/legacy", Summary: "Старые ссылки подписки", Tags: tags}, h.getLegacy)
	huma.Register(h.api, huma.Operation{OperationID: "update-legacy-links", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPatch, Path: "/api/v1/import/legacy", Summary: "Настроить старые ссылки подписки", Tags: tags}, h.updateLegacy)
}

// importError turns a failed read of the old panel into the field it is about.
func importError(src importSource, err error) error {
	code := panelimport.Code(err)
	field := "body.url"
	if errors.Is(err, panelimport.ErrAuth) {
		field = "body.password"
		if src.Kind == string(panelimport.Remnawave) || src.Token != "" {
			field = "body.token"
		}
	}
	detail := &huma.ErrorDetail{Location: field, Message: code}
	if errors.Is(err, panelimport.ErrAnswer) || errors.Is(err, panelimport.ErrTLS) || errors.Is(err, panelimport.ErrRedirect) || errors.Is(err, panelimport.ErrAddress) {
		detail.Value = err.Error()
	}
	return huma.Error422UnprocessableEntity(code, detail)
}

func (h *handlers) importPreview(ctx context.Context, in *importPreviewInput) (*importPreviewOutput, error) {
	if h.d.Importer == nil {
		return nil, huma.Error503ServiceUnavailable("import_failed")
	}
	users, err := panelimport.Fetch(ctx, h.d.Importer.HTTP(), in.Body.source())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, importError(in.Body, err)
	}
	p, err := panelimport.Check(ctx, h.d.Store.Q, users)
	if err != nil {
		return nil, err
	}
	return &importPreviewOutput{Body: p}, nil
}

func (h *handlers) importRun(ctx context.Context, in *importRunInput) (*importStateOutput, error) {
	if h.d.Importer == nil {
		return nil, huma.Error503ServiceUnavailable("import_failed")
	}
	b := in.Body
	src := importSource{Kind: b.Kind, URL: b.URL, Username: b.Username, Password: b.Password, Token: b.Token}
	if _, err := h.d.Store.Q.GetTariff(ctx, b.TariffID); err != nil {
		return nil, huma.Error422UnprocessableEntity("tariff_not_found", &huma.ErrorDetail{Location: "body.tariff_id", Message: "tariff_not_found"})
	}
	if err := h.d.Importer.Start(src.source(), b.TariffID, src.host()); errors.Is(err, panelimport.ErrBusy) {
		return nil, huma.Error409Conflict(err.Error())
	} else if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "import.run", "users", "", map[string]any{"from": src.host(), "tariff_id": b.TariffID})
	return &importStateOutput{Body: h.d.Importer.State()}, nil
}

func (h *handlers) importStatus(ctx context.Context, _ *struct{}) (*importStateOutput, error) {
	if h.d.Importer == nil {
		return &importStateOutput{Body: panelimport.JobState{State: "idle"}}, nil
	}
	return &importStateOutput{Body: h.d.Importer.State()}, nil
}

func (h *handlers) getLegacy(ctx context.Context, _ *struct{}) (*legacyOutput, error) {
	var v LegacyView
	var err error
	if v.Path, err = h.d.Settings.String(ctx, settings.KeyLegacySubPath); err != nil {
		return nil, err
	}
	if v.Kind, err = h.d.Settings.String(ctx, settings.KeyLegacySubKind); err != nil {
		return nil, err
	}
	secret, err := h.d.Settings.String(ctx, settings.KeyLegacySubSecret)
	if err != nil {
		return nil, err
	}
	v.SecretSet = secret != ""
	if v.Links, err = h.d.Store.Q.CountLegacySubTokens(ctx); err != nil {
		return nil, err
	}
	return &legacyOutput{Body: v}, nil
}

// legacyPath is one to three segments of letters, digits, "-" and "_": "sub", "api/sub".
var legacyPath = regexp.MustCompile(`^[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+){0,2}$`)

func (h *handlers) updateLegacy(ctx context.Context, in *patchLegacyInput) (*legacyOutput, error) {
	b := in.Body
	if b.Path != nil {
		p := strings.Trim(strings.TrimSpace(*b.Path), "/")
		if p != "" {
			paths, err := h.d.Settings.Paths(ctx)
			if err != nil {
				return nil, err
			}
			first, _, _ := strings.Cut(p, "/")
			if !legacyPath.MatchString(p) || first == paths.Admin || first == paths.Sub {
				return nil, huma.Error422UnprocessableEntity("legacy_path_invalid", &huma.ErrorDetail{Location: "body.path", Message: "legacy_path_invalid"})
			}
		}
		b.Path = &p
	}
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		set := settings.New(q)
		for key, v := range map[string]*string{settings.KeyLegacySubPath: b.Path, settings.KeyLegacySubKind: b.Kind, settings.KeyLegacySubSecret: b.Secret} {
			if v == nil {
				continue
			}
			if err := settings.Set(ctx, set, key, strings.TrimSpace(*v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	details := map[string]any{}
	if b.Path != nil {
		details["path"] = *b.Path
	}
	if b.Kind != nil {
		details["kind"] = *b.Kind
	}
	if b.Secret != nil {
		details["secret"] = "changed"
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "import.legacy", "settings", "", details)
	return h.getLegacy(ctx, nil)
}
