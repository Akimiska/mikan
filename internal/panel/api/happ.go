package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type happLinkOutput struct {
	Body struct {
		Link string `json:"link" doc:"happ://crypt5/…: Happ открывает подписку, не показывая её адрес; пусто — шифрованная ссылка выключена"`
	}
}

func (h *handlers) registerHapp() {
	huma.Register(h.api, huma.Operation{OperationID: "user-happ-link", Method: http.MethodGet, Path: "/api/v1/users/{id}/happ-link",
		Summary: "Шифрованная ссылка Happ на подписку пользователя", Tags: []string{"users"}}, h.userHappLink)
}

// userHappLink: the link opens the subscription like the address does, so a key that may
// only read does not get it, as it does not get the address.
func (h *handlers) userHappLink(ctx context.Context, in *userIDInput) (*happLinkOutput, error) {
	if hidesLinks(ctx) {
		if _, ok := supportGrantOf(ctx); ok {
			return nil, huma.Error403Forbidden("support_denied")
		}
		return nil, huma.Error403Forbidden("read_key")
	}
	u, err := h.d.Store.Q.GetUser(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("user_not_found")
	}
	if err != nil {
		return nil, err
	}
	out := &happLinkOutput{}
	if h.d.HappLink == nil {
		return out, nil
	}
	if out.Body.Link, err = h.d.HappLink(ctx, u); err != nil {
		h.d.Log.Warn("happ link", "user", u.ID, "err", err)
		return nil, huma.Error502BadGateway("happ_link")
	}
	return out, nil
}
