package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgbot"
)

const (
	ykShop   = "506751"
	ykSecret = "test_Fh8hUAVVBGUGbjmlzba6TB0iyUbos_lueTHE-axOwM0"
)

// ykFake is YooKassa as far as the panel uses it.
type ykFake struct {
	mu   sync.Mutex
	pays map[string]map[string]any
}

func (f *ykFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u != ykShop || p != ykSecret {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","code":"invalid_credentials"}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/me":
		_, _ = w.Write([]byte(`{"account_id":"` + ykShop + `"}`))
	case r.Method == http.MethodPost && r.URL.Path == "/payments":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		id := "yk-" + strconv.Itoa(len(f.pays)+1)
		p := map[string]any{"id": id, "status": "pending", "paid": false, "amount": in["amount"], "metadata": in["metadata"],
			"confirmation": map[string]any{"type": "redirect", "confirmation_url": "https://yoomoney.ru/checkout/" + id}}
		f.pays[id] = p
		_ = json.NewEncoder(w).Encode(p)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/payments/"):
		_ = json.NewEncoder(w).Encode(f.pays[strings.TrimPrefix(r.URL.Path, "/payments/")])
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// Payments over HTTP: the keys are checked before they are saved and never come back,
// only an admin session sets them, the Mini App sells to the signed-in account only.
func TestPaymentsOverHTTP(t *testing.T) {
	yk := &ykFake{pays: map[string]map[string]any{}}
	srv := httptest.NewServer(yk)
	t.Cleanup(srv.Close)
	h := newHarness(t, func(o *Options) { o.YooKassaAPI = srv.URL })
	ctx := context.Background()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	set := settings.New(h.st.Q)
	for k, v := range map[string]any{settings.KeyPublicHost: "203.0.113.10", settings.KeyPanelPort: 21355, tgbot.KeyToken: tgToken} {
		if err := settings.Set(ctx, set, k, v); err != nil {
			t.Fatal(err)
		}
	}
	ts, _ := h.st.Q.ListTariffs(ctx)
	sale, err := h.st.Q.UpdateTariff(ctx, db.UpdateTariffParams{Name: "Месяц", TrafficLimit: ts[1].TrafficLimit, DurationDays: 30, DeviceLimit: ts[1].DeviceLimit,
		ResetStrategy: ts[1].ResetStrategy, PriceRub: sql.NullInt64{Int64: 19900, Valid: true}, OnSale: 1, ID: ts[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	api := "/" + adminPath + "/api/v1"
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}

	// A tariff on sale needs a price.
	if resp, body := h.do(http.MethodPut, api+"/tariffs/"+strconv.FormatInt(sale.ID, 10), map[string]any{"name": "x", "duration_days": 30, "reset_strategy": "none", "on_sale": true}, csrf); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "on_sale_no_price") {
		t.Fatalf("on sale without a price: %d %s", resp.StatusCode, body)
	}
	for name, c := range map[string]struct {
		body map[string]any
		code string
	}{
		"on without keys":   {map[string]any{"yookassa": true}, "yookassa_not_configured"},
		"letters in shopId": {map[string]any{"yookassa_shop_id": "abc"}, "shop_id_invalid"},
		"wrong secret":      {map[string]any{"yookassa_shop_id": ykShop, "yookassa_secret": "test_wrong"}, "yookassa_keys_invalid"},
	} {
		if resp, body := h.do(http.MethodPatch, api+"/payments/settings", c.body, csrf); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), c.code) {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
	}
	// Selling starts off, and the only tariff on sale has a ruble price anyway: the bot sells
	// nothing, and the Payments page says so.
	if resp, body := h.do(http.MethodGet, api+"/payments/settings", nil, nil); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"on_sale":0`) || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("nothing on sale: %d %s", resp.StatusCode, body)
	}
	resp, body := h.do(http.MethodPatch, api+"/payments/settings", map[string]any{"enabled": true, "yookassa": true, "yookassa_shop_id": ykShop, "yookassa_secret": ykSecret}, csrf)
	var ps struct {
		SecretSet bool   `json:"yookassa_secret_set"`
		OnSale    int    `json:"on_sale"`
		Webhook   string `json:"webhook_yookassa"`
		Available struct {
			YooKassa bool `json:"yookassa"`
		} `json:"available"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &ps) != nil || !ps.SecretSet || !ps.Available.YooKassa || ps.OnSale != 1 || strings.Contains(string(body), ykSecret) ||
		!strings.HasPrefix(ps.Webhook, "https://203.0.113.10:21355/"+subPath+"/pay/yookassa/") {
		t.Fatalf("settings: %d %s", resp.StatusCode, body)
	}

	// A key may read payments but never touch where the money goes.
	resp, body = h.do(http.MethodPost, api+"/api-keys", map[string]any{"name": "billing", "scope": "full"}, csrf)
	var key struct {
		Key string `json:"key"`
	}
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &key) != nil {
		t.Fatalf("key: %d %s", resp.StatusCode, body)
	}
	bearer := map[string]string{"Authorization": "Bearer " + key.Key}
	if resp, _ := h.do(http.MethodGet, api+"/payments", nil, bearer); resp.StatusCode != http.StatusOK {
		t.Fatalf("key reads payments: %d", resp.StatusCode)
	}
	for _, m := range []string{http.MethodGet, http.MethodPatch} {
		if resp, _ := h.do(m, api+"/payments/settings", map[string]any{"yookassa_secret": "test_mine"}, bearer); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s payment settings with a key: %d", m, resp.StatusCode)
		}
	}

	// The Mini App: the plans, then an invoice for the signed-in account.
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	shop := "/" + subPath + "/tg/shop"
	pay := "/" + subPath + "/tg/pay"
	resp, body = h.do(http.MethodPost, shop, map[string]any{"init_data": initData(tgToken, 555, h.now)}, same)
	var offers struct {
		Offers []struct {
			ID  int64 `json:"id"`
			Rub int64 `json:"rub"`
		} `json:"offers"`
		Providers map[string]bool `json:"providers"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &offers) != nil || len(offers.Offers) != 1 || offers.Offers[0].Rub != 19900 || !offers.Providers["yookassa"] || offers.Providers["stars"] {
		t.Fatalf("shop: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(http.MethodPost, shop, map[string]any{"init_data": initData("987654321:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw1", 555, h.now)}, same); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("another bot's signature: %d", resp.StatusCode)
	}
	if resp, _ := h.do(http.MethodPost, pay, map[string]any{"init_data": initData(tgToken, 555, h.now), "tariff_id": sale.ID, "provider": "yookassa"}, map[string]string{"Sec-Fetch-Site": "cross-site"}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("from another site: %d", resp.StatusCode)
	}
	// Renewing someone else's subscription by its token is refused.
	clock := h.p.now
	other, err := domain.NewUsers(h.st, domain.NewPool(h.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "other", TariffID: sale.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := h.do(http.MethodPost, pay, map[string]any{"init_data": initData(tgToken, 555, h.now), "tariff_id": sale.ID, "provider": "yookassa", "token": other.SubToken}, same); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("someone else's subscription: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodPost, pay, map[string]any{"init_data": initData(tgToken, 555, h.now), "tariff_id": sale.ID, "provider": "yookassa"}, same)
	var inv struct {
		URL string `json:"url"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &inv) != nil || !strings.HasPrefix(inv.URL, "https://yoomoney.ru/checkout/") {
		t.Fatalf("pay: %d %s", resp.StatusCode, body)
	}

	// A notification from outside YooKassa's networks is refused at the door.
	resp, _ = h.do(http.MethodPost, strings.TrimPrefix(ps.Webhook, "https://203.0.113.10:21355"), map[string]any{"event": "payment.succeeded", "object": map[string]any{"id": "yk-1"}}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("webhook from 127.0.0.1: %d", resp.StatusCode)
	}
	// Paid at YooKassa: the reconcile pass applies it and the history shows it.
	yk.mu.Lock()
	yk.pays["yk-1"]["status"], yk.pays["yk-1"]["paid"] = "succeeded", true
	yk.mu.Unlock()
	h.p.Billing.Reconcile(ctx)
	resp, body = h.do(http.MethodGet, api+"/payments?status=applied", nil, nil)
	var hist struct {
		Items []struct {
			ID       int64  `json:"id"`
			UserName string `json:"user_name"`
			Kind     string `json:"kind"`
		} `json:"items"`
		Totals []struct {
			Currency string `json:"currency"`
			Total    int64  `json:"total"`
		} `json:"totals"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &hist) != nil || len(hist.Items) != 1 || hist.Items[0].Kind != "new" || hist.Items[0].UserName == "" ||
		len(hist.Totals) != 1 || hist.Totals[0].Total != 19900 {
		t.Fatalf("history: %d %s", resp.StatusCode, body)
	}
	// Only Stars are refunded from the panel.
	if resp, _ := h.do(http.MethodPost, api+"/payments/"+strconv.FormatInt(hist.Items[0].ID, 10)+"/refund", nil, csrf); resp.StatusCode != http.StatusConflict {
		t.Fatalf("refund of a card payment: %d", resp.StatusCode)
	}
}
