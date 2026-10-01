package billing

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"mikan/internal/panel/store/db"
)

// providerError is a provider's refusal: Code is short and safe to show and store.
type providerError struct {
	Code   string
	Status int
}

func (e *providerError) Error() string { return fmt.Sprintf("%s (HTTP %d)", e.Code, e.Status) }

// yooKassa is the YooKassa API v3: Basic auth with the shop id and the secret key.
type yooKassa struct {
	base, shopID, secret string
	hc                   *http.Client
}

type ykAmount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

type ykPayment struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"` // pending | waiting_for_capture | succeeded | canceled
	Paid         bool              `json:"paid"`
	Amount       ykAmount          `json:"amount"`
	Metadata     map[string]string `json:"metadata"`
	Confirmation struct {
		URL string `json:"confirmation_url"`
	} `json:"confirmation"`
}

func (y yooKassa) do(ctx context.Context, method, path, idem string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, y.base+path, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(y.shopID, y.secret)
	req.Header.Set("Content-Type", "application/json")
	if idem != "" {
		req.Header.Set("Idempotence-Key", idem)
	}
	resp, err := y.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Code == "" {
			e.Code = "yookassa_error"
		}
		return &providerError{Code: "yookassa_" + strings.TrimPrefix(e.Code, "yookassa_"), Status: resp.StatusCode}
	}
	return json.Unmarshal(raw, out)
}

// create opens a payment with a redirect to YooKassa's page. The payload is both the
// idempotence key (a retry never charges twice) and the metadata we check on the way back.
func (y yooKassa) create(ctx context.Context, payload string, kopecks int64, description, returnURL string) (id, url string, err error) {
	if returnURL == "" {
		returnURL = "https://t.me"
	}
	var p ykPayment
	err = y.do(ctx, http.MethodPost, "/payments", payload, map[string]any{
		"amount":       ykAmount{Value: rubles(kopecks), Currency: "RUB"},
		"capture":      true,
		"confirmation": map[string]string{"type": "redirect", "return_url": returnURL},
		"description":  truncate(description, 128),
		"metadata":     map[string]string{"payload": payload},
	}, &p)
	if err != nil {
		return "", "", err
	}
	if p.ID == "" || p.Confirmation.URL == "" {
		return "", "", &providerError{Code: "yookassa_bad_response", Status: 200}
	}
	return p.ID, p.Confirmation.URL, nil
}

func (y yooKassa) get(ctx context.Context, id string) (ykPayment, error) {
	var p ykPayment
	if id == "" || strings.ContainsAny(id, "/?#") {
		return p, &providerError{Code: "yookassa_bad_id"}
	}
	err := y.do(ctx, http.MethodGet, "/payments/"+id, "", nil, &p)
	return p, err
}

// rubles: 19900 → "199.00".
func rubles(kopecks int64) string { return fmt.Sprintf("%d.%02d", kopecks/100, kopecks%100) }

// kopecks: "199.00" → 19900; -1 when it is not a sum of money.
func kopecks(s string) int64 {
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return -1
	}
	frac += strings.Repeat("0", 2-len(frac))
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil || w < 0 {
		return -1
	}
	return w*100 + f
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// yooKassaNets are where YooKassa sends notifications from
// (https://yookassa.ru/developers/using-api/webhooks#ip).
var yooKassaNets = []netip.Prefix{
	netip.MustParsePrefix("185.71.76.0/27"),
	netip.MustParsePrefix("185.71.77.0/27"),
	netip.MustParsePrefix("77.75.153.0/25"),
	netip.MustParsePrefix("77.75.156.11/32"),
	netip.MustParsePrefix("77.75.156.35/32"),
	netip.MustParsePrefix("77.75.154.128/25"),
	netip.MustParsePrefix("2a02:5180::/32"),
}

func fromYooKassa(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, n := range yooKassaNets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

// checkYooKassa asks YooKassa about a payment and records what it says. Nothing in a
// notification is trusted: only this answer, made with the shop's own key.
func (s *Service) checkYooKassa(ctx context.Context, id string) error {
	c, sec := s.Config(ctx), s.secrets(ctx)
	if c.ShopID == "" || sec.YooKassaSecret == "" {
		return ErrProviderOff
	}
	pay, err := s.d.Store.Q.GetPaymentByExternal(ctx, db.GetPaymentByExternalParams{Provider: YooKassa, ExternalID: sql.NullString{String: id, Valid: true}})
	if err != nil {
		return ErrBadPayment
	}
	y := yooKassa{base: s.d.YooKassaAPI, shopID: c.ShopID, secret: sec.YooKassaSecret, hc: s.d.HTTP}
	p, err := y.get(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case p.ID != id || p.Metadata["payload"] != pay.Payload:
		s.d.Log.Error("billing: yookassa payment does not match", "payment", pay.ID)
		return ErrBadPayment
	case p.Status == "succeeded" && p.Paid:
		if p.Amount.Currency != pay.Currency || kopecks(p.Amount.Value) != pay.Amount {
			s.d.Log.Error("billing: yookassa amount differs", "payment", pay.ID, "got", p.Amount.Value, "currency", p.Amount.Currency)
			return ErrBadPayment
		}
		return s.paid(ctx, pay, id)
	case p.Status == "canceled":
		_, err := s.d.Store.Q.SetPaymentStatus(ctx, db.SetPaymentStatusParams{NewStatus: "failed", ID: pay.ID, OldStatus: "pending"})
		return err
	}
	return nil
}
