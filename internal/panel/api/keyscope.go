package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// What an API key (and a support operator) may see and do beyond the endpoint it is let
// into (apikeys.go keeps the keys themselves, support.go the support grants).

// requireSession refuses an API key on a change of what a leaked "full" key must not be
// able to turn against the admin: where clients are sent, the secret paths. The change
// is one field of an endpoint a key may otherwise call, so the check is by field; field
// names it in the answer. A support operator is refused as well, at every level: these
// fields are the admin's alone (a terminal grant is the way to the rest).
func requireSession(ctx context.Context, field string) error {
	if _, ok := apiKeyOf(ctx); ok {
		return huma.Error403Forbidden("session_only", &huma.ErrorDetail{Location: "body." + field, Message: "session_only"})
	}
	if _, ok := supportGrantOf(ctx); ok {
		return huma.Error403Forbidden("support_denied", &huma.ErrorDetail{Location: "body." + field, Message: "support_denied"})
	}
	return nil
}

// hidesSecrets: a "read" key may look at the panel, not take it over; neither may a support
// operator, whatever its level. What would hand the reader other people's credentials or the
// way into the admin (the secret paths, an own profile) stays out of its answers. The
// subscribers' links are hidesLinks: the admin may let an operator see them.
func hidesSecrets(ctx context.Context) bool {
	if k, ok := apiKeyOf(ctx); ok {
		return k.Scope != "full"
	}
	_, ok := supportGrantOf(ctx)
	return ok
}
