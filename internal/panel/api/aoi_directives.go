package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/aoiapp"
)

// The AoiVPN fork's directives to the installed apps (aoiapp.Directives): move the
// subscription links to another address, add or remove a subscription in an app, update
// the profiles now, show a message. Sending one is the admin's own session only: a
// directive puts a link in every app it reaches.

type aoiDirectiveBody struct {
	Kind   string `json:"kind" enum:"rewrite_url,add_profile,remove_profile,refresh,message"`
	Target string `json:"target" enum:"all,user,app" doc:"Кому: всем приложениям, установкам подписки (user_id) или одной установке (app_id)"`
	UserID int64  `json:"user_id,omitempty" doc:"target=user: подписка, чьим установкам"`
	AppID  string `json:"app_id,omitempty" maxLength:"64" doc:"target=app: установка"`
	From   string `json:"from,omitempty" maxLength:"300" doc:"rewrite_url: начало ссылки, которое заменить"`
	To     string `json:"to,omitempty" maxLength:"300" doc:"rewrite_url: на что заменить"`
	URL    string `json:"url,omitempty" maxLength:"500" doc:"add_profile: ссылка подписки; пусто — ссылка подписки sub_user_id"`
	Label  string `json:"label,omitempty" maxLength:"100"`
	// The subscription a profile is added, removed or updated for: the panel puts in its link
	// and its tokens, legacy ones too.
	SubUserID int64  `json:"sub_user_id,omitempty" doc:"add_profile/remove_profile/refresh: подписка, о которой указание"`
	Title     string `json:"title,omitempty" maxLength:"100"`
	Text      string `json:"text,omitempty" maxLength:"2000"`
	Note      string `json:"note,omitempty" maxLength:"300" doc:"Заметка администратора"`
	Hours     int64  `json:"expires_hours,omitempty" minimum:"0" maximum:"8760" doc:"Сколько часов выдавать; 0 — пока не отменено"`
}

type aoiDirectiveOutput struct{ Body aoiapp.Directive }

type aoiDirectivesListInput struct {
	Limit  int64 `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int64 `query:"offset" minimum:"0" default:"0"`
}

type aoiDirectivesListOutput struct {
	Body struct {
		Items []aoiapp.Directive `json:"items"`
		Total int64              `json:"total"`
	}
}

type aoiDirectiveIDInput struct {
	ID int64 `path:"id"`
}

type aoiDirectiveAcksOutput struct {
	Body struct {
		Items []aoiapp.Ack `json:"items"`
	}
}

type aoiInstallsInput struct {
	UserID int64  `query:"user_id" minimum:"0" doc:"Установки этой подписки"`
	Q      string `query:"q" maxLength:"64" doc:"Часть app_id"`
	Limit  int64  `query:"limit" minimum:"1" maximum:"500" default:"100"`
}

type aoiInstallsOutput struct {
	Body struct {
		Items []aoiapp.Install `json:"items"`
	}
}

func (h *handlers) registerAoiDirectives() {
	tags := []string{"aoi-directives"}
	huma.Register(h.api, huma.Operation{OperationID: "list-aoi-directives", Method: http.MethodGet, Path: "/api/v1/aoi/directives", Summary: "Указания приложениям AoiVPN", Tags: tags}, h.listAoiDirectives)
	huma.Register(h.api, huma.Operation{OperationID: "create-aoi-directive", Method: http.MethodPost, Path: "/api/v1/aoi/directives", Summary: "Отправить указание приложениям AoiVPN", Tags: tags, DefaultStatus: http.StatusCreated}, h.createAoiDirective)
	huma.Register(h.api, huma.Operation{OperationID: "cancel-aoi-directive", Method: http.MethodPost, Path: "/api/v1/aoi/directives/{id}/cancel", Summary: "Отменить указание", Tags: tags}, h.cancelAoiDirective)
	huma.Register(h.api, huma.Operation{OperationID: "aoi-directive-acks", Method: http.MethodGet, Path: "/api/v1/aoi/directives/{id}/acks", Summary: "Кто выполнил указание", Tags: tags}, h.aoiDirectiveAcks)
	huma.Register(h.api, huma.Operation{OperationID: "list-aoi-installs", Method: http.MethodGet, Path: "/api/v1/aoi/installs", Summary: "Установки приложения AoiVPN", Tags: tags}, h.listAoiInstalls)
}

func (h *handlers) aoiDirectives() (*aoiapp.Directives, error) {
	if h.d.AoiDirectives == nil {
		return nil, huma.Error503ServiceUnavailable("directives_unavailable")
	}
	return h.d.AoiDirectives, nil
}

func (h *handlers) listAoiDirectives(ctx context.Context, in *aoiDirectivesListInput) (*aoiDirectivesListOutput, error) {
	d, err := h.aoiDirectives()
	if err != nil {
		return nil, err
	}
	items, total, err := d.List(ctx, in.Limit, in.Offset)
	if err != nil {
		return nil, err
	}
	out := &aoiDirectivesListOutput{}
	out.Body.Items, out.Body.Total = items, total
	return out, nil
}

func (h *handlers) createAoiDirective(ctx context.Context, in *struct{ Body aoiDirectiveBody }) (*aoiDirectiveOutput, error) {
	if err := requireOwner(ctx, "kind"); err != nil {
		return nil, err
	}
	d, err := h.aoiDirectives()
	if err != nil {
		return nil, err
	}
	b := in.Body
	n := aoiapp.NewDirective{Kind: b.Kind, Target: b.Target, UserID: b.UserID, AppID: strings.TrimSpace(b.AppID), Note: b.Note,
		Expires: time.Duration(b.Hours) * time.Hour,
		Params:  aoiapp.DirectiveParams{From: b.From, To: b.To, URL: b.URL, Label: b.Label, Title: b.Title, Text: b.Text}}
	if b.SubUserID > 0 && b.Kind != aoiapp.KindRewriteURL && b.Kind != aoiapp.KindMessage {
		tokens, err := d.UserTokens(ctx, b.SubUserID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("user_not_found")
		}
		if err != nil {
			return nil, err
		}
		n.Params.Tokens = tokens
		if b.Kind == aoiapp.KindAddProfile && strings.TrimSpace(b.URL) == "" {
			if len(tokens) == 0 || h.d.SubBase == nil || h.d.SubBase(ctx) == "" {
				return nil, huma.Error400BadRequest("no_sub_url")
			}
			n.Params.URL = h.d.SubBase(ctx) + "/" + tokens[0]
		}
	}
	v, err := d.Create(ctx, n)
	if de := (*aoiapp.DirectiveError)(nil); errors.As(err, &de) {
		return nil, huma.Error400BadRequest(de.Code)
	}
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "aoi_directive.create", "aoi_directive", strconv.FormatInt(v.ID, 10), map[string]any{"kind": v.Kind, "target": v.Target, "user_id": v.UserID, "app_id": v.AppID})
	return &aoiDirectiveOutput{Body: v}, nil
}

func (h *handlers) cancelAoiDirective(ctx context.Context, in *aoiDirectiveIDInput) (*aoiDirectiveOutput, error) {
	if err := requireOwner(ctx, "id"); err != nil {
		return nil, err
	}
	d, err := h.aoiDirectives()
	if err != nil {
		return nil, err
	}
	v, err := d.Cancel(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("not_found")
	}
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "aoi_directive.cancel", "aoi_directive", strconv.FormatInt(v.ID, 10), nil)
	return &aoiDirectiveOutput{Body: v}, nil
}

func (h *handlers) aoiDirectiveAcks(ctx context.Context, in *aoiDirectiveIDInput) (*aoiDirectiveAcksOutput, error) {
	d, err := h.aoiDirectives()
	if err != nil {
		return nil, err
	}
	items, err := d.Acks(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out := &aoiDirectiveAcksOutput{}
	out.Body.Items = items
	return out, nil
}

func (h *handlers) listAoiInstalls(ctx context.Context, in *aoiInstallsInput) (*aoiInstallsOutput, error) {
	d, err := h.aoiDirectives()
	if err != nil {
		return nil, err
	}
	items, err := d.Installs(ctx, in.UserID, in.Q, in.Limit)
	if err != nil {
		return nil, err
	}
	out := &aoiInstallsOutput{}
	out.Body.Items = items
	return out, nil
}
