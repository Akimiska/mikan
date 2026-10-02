package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/panelimport"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// Users from another panel (panelimport). Everything here is the session's: it reads the
// old panel with its admin's password and creates users here.

type importSource struct {
	Kind     string `json:"kind" enum:"marzban,pasarguard,remnawave"`
	URL      string `json:"url" maxLength:"300" doc:"Адрес старой панели: https://panel.example.com"`
	Username string `json:"username,omitempty" maxLength:"200" doc:"Marzban, PasarGuard: логин администратора"`
	Password string `json:"password,omitempty" maxLength:"500"`
	Token    string `json:"token,omitempty" maxLength:"4096" doc:"Remnawave: API-токен; PasarGuard: API-ключ вместо логина"`
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
		TariffID int64  `json:"tariff_id" minimum:"1" doc:"Тариф, на котором появятся пользователи; лимит, срок и устройства берутся из старой панели"`
	}
}

type importRunOutput struct{ Body panelimport.Report }

type LegacyView struct {
	Path      string `json:"path" doc:"Путь старых ссылок подписки: sub у Marzban и PasarGuard, api/sub у Remnawave; пусто — выключено"`
	Kind      string `json:"kind" doc:"Чьи подписанные ссылки проверять: marzban или pasarguard"`
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
	huma.Register(h.api, huma.Operation{OperationID: "import-run", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/import", Summary: "Перенести пользователей из другой панели", Tags: tags}, h.importRun)
	huma.Register(h.api, huma.Operation{OperationID: "get-legacy-links", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodGet, Path: "/api/v1/import/legacy", Summary: "Старые ссылки подписки", Tags: tags}, h.getLegacy)
	huma.Register(h.api, huma.Operation{OperationID: "update-legacy-links", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPatch, Path: "/api/v1/import/legacy", Summary: "Настроить старые ссылки подписки", Tags: tags}, h.updateLegacy)
}

// importClient reads the old panel. Its address is the admin's own word, and the old
// panel often runs on this very server, so local addresses are allowed.
var importClient = &http.Client{Timeout: 2 * time.Minute}

func (h *handlers) fetchImport(ctx context.Context, src importSource) ([]panelimport.User, error) {
	users, err := panelimport.Fetch(ctx, importClient, panelimport.Source{Kind: panelimport.Kind(src.Kind), URL: src.URL,
		Username: src.Username, Password: src.Password, Token: src.Token})
	switch {
	case errors.Is(err, panelimport.ErrAuth):
		field := "body.password"
		if src.Kind == string(panelimport.Remnawave) || src.Token != "" {
			field = "body.token"
		}
		return nil, huma.Error422UnprocessableEntity("import_auth", &huma.ErrorDetail{Location: field, Message: "import_auth"})
	case errors.Is(err, panelimport.ErrUnreachable):
		return nil, huma.Error422UnprocessableEntity("import_unreachable", &huma.ErrorDetail{Location: "body.url", Message: "import_unreachable"})
	case errors.Is(err, panelimport.ErrAnswer):
		return nil, huma.Error422UnprocessableEntity("import_bad_answer", &huma.ErrorDetail{Location: "body.url", Message: "import_bad_answer", Value: err.Error()})
	}
	return users, err
}

func (h *handlers) importPreview(ctx context.Context, in *importPreviewInput) (*importPreviewOutput, error) {
	users, err := h.fetchImport(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	p, err := panelimport.Check(ctx, h.d.Store.Q, users)
	if err != nil {
		return nil, err
	}
	return &importPreviewOutput{Body: p}, nil
}

func (h *handlers) importRun(ctx context.Context, in *importRunInput) (*importRunOutput, error) {
	b := in.Body
	users, err := h.fetchImport(ctx, importSource{Kind: b.Kind, URL: b.URL, Username: b.Username, Password: b.Password, Token: b.Token})
	if err != nil {
		return nil, err
	}
	r, err := panelimport.Apply(ctx, h.d.Store, h.d.Users, h.d.Now(), panelimport.Kind(in.Body.Kind), in.Body.TariffID, users)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, huma.Error422UnprocessableEntity("tariff_not_found", &huma.ErrorDetail{Location: "body.tariff_id", Message: "tariff_not_found"})
	}
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "import.run", "users", "", map[string]any{"from": in.Body.Kind, "created": r.Created, "skipped": len(r.Skipped), "failed": len(r.Failed)})
	return &importRunOutput{Body: r}, nil
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
