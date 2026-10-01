package billing

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"mikan/internal/panel/secure"
)

// Webhook serves the providers' notifications at <sub path>/pay/<provider>/<token>, and
// at <sub path>/pay/addon/<id>/<token> for the adapters. The token keeps strangers from
// even reaching the checks; each provider then has its own: YooKassa by source address and
// a second look at the payment through its API, CryptoBot by the HMAC signature and its
// API, an adapter by what its provider offers and a second look through the adapter.
func (s *Service) Webhook() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider, token, ok := strings.Cut(strings.Trim(r.URL.Path, "/"), "/")
		addon := ""
		if provider == "addon" {
			addon, token, ok = strings.Cut(token, "/")
			ok = ok && AddonID(AddonPrefix+addon) != ""
		}
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
		// The provider's call is answered now; checking with its API may take a while
		// and the provider retries on a slow answer.
		ctx := context.WithoutCancel(r.Context())
		if addon != "" {
			s.addonWebhook(ctx, w, r, addon, body)
			return
		}
		switch provider {
		case YooKassa:
			if !fromYooKassa(s.clientIP(r)) {
				s.d.Log.Warn("billing: yookassa webhook from a foreign address", "ip", s.clientIP(r))
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			var n struct {
				Event  string `json:"event"`
				Object struct {
					ID string `json:"id"`
				} `json:"object"`
			}
			if json.Unmarshal(body, &n) != nil || n.Object.ID == "" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if strings.HasPrefix(n.Event, "payment.") {
				if err := s.checkYooKassa(ctx, n.Object.ID); err != nil && err != ErrBadPayment {
					// Not answered 200: YooKassa sends it again.
					http.Error(w, "retry", http.StatusServiceUnavailable)
					return
				}
			}
		case CryptoBot:
			if !validCryptoSignature(s.secrets(ctx).CryptoBotToken, body, r.Header.Get("Crypto-Pay-Api-Signature")) {
				s.d.Log.Warn("billing: cryptobot webhook with a bad signature", "ip", s.clientIP(r))
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			var u cryptoUpdate
			if json.Unmarshal(body, &u) != nil || u.Payload.ID == 0 {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if u.Type == "invoice_paid" {
				if err := s.checkCryptoBot(ctx, strconv.FormatInt(u.Payload.ID, 10)); err != nil && err != ErrBadPayment {
					http.Error(w, "retry", http.StatusServiceUnavailable)
					return
				}
			}
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
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

// WebhookURLs are what the admin enters in the providers' dashboards.
func (s *Service) WebhookURLs(ctx context.Context, subBase string) (yookassa, cryptobot string) {
	tok, err := s.WebhookToken(ctx)
	if err != nil || subBase == "" {
		return "", ""
	}
	return subBase + "/pay/" + YooKassa + "/" + tok, subBase + "/pay/" + CryptoBot + "/" + tok
}
