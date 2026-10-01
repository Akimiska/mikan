package billing

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"

	"mikan/internal/panel/secure"
)

// Webhook serves the providers' notifications at <sub path>/pay/addon/<id>/<token>. The
// token keeps strangers from even reaching the checks; the adapter then checks what its
// provider offers (a signature, the source address) and the panel asks it about the
// invoice before it trusts anything. <sub path>/pay/yookassa/<token> and
// /pay/cryptobot/<token>, the URLs of the built-in providers that admins set in those
// dashboards before 0.4.4, lead to the adapters that took over.
func (s *Service) Webhook() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider, token, ok := strings.Cut(strings.Trim(r.URL.Path, "/"), "/")
		var addon string
		switch provider {
		case "addon":
			addon, token, ok = strings.Cut(token, "/")
		case legacyYooKassa, legacyCryptoBot:
			addon = provider
		}
		ok = ok && AddonID(AddonPrefix+addon) != ""
		want, err := s.WebhookToken(r.Context())
		if r.Method != http.MethodPost || !ok || err != nil || !secure.Equal(token, want) {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// The provider's call is answered now; asking the adapter may take a while and the
		// provider retries on a slow answer.
		s.addonWebhook(context.WithoutCancel(r.Context()), w, r, addon, body)
	})
}

func (s *Service) clientIP(r *http.Request) string {
	if s.d.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
