package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

const (
	shopID   = "123456"
	ykSecret = "live_ykSECRETvalue000"
	cbToken  = "12345:AAcryptoTOKENvalue"
)

type noChanges struct{}

func (noChanges) PoliciesChanged() {}
func (noChanges) SlotsChanged()    {}

// fakeTG is the bot: Stars links, refunds and what buyers were told.
type fakeTG struct {
	mu      sync.Mutex
	paid    []db.Payment
	refunds []string
}

func (f *fakeTG) InvoiceLink(_ context.Context, _, _, payload string, stars int64) (string, error) {
	return "https://t.me/$" + payload[:8] + "?stars=" + strconv.FormatInt(stars, 10), nil
}
func (f *fakeTG) RefundStars(_ context.Context, _ int64, charge string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refunds = append(f.refunds, charge)
	return nil
}
func (f *fakeTG) Paid(_ context.Context, p db.Payment, _ db.User, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paid = append(f.paid, p)
}
func (f *fakeTG) BotURL(context.Context) string { return "https://t.me/mikan_test_bot" }
func (f *fakeTG) told() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.paid)
}

// fakeYooKassa answers like the API with whatever the test put in payments.
type fakeYooKassa struct {
	mu       sync.Mutex
	payments map[string]*ykPayment
	idem     map[string]string
	n        int
}

func (f *fakeYooKassa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || user != shopID || pass != ykSecret {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","code":"invalid_credentials"}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/payments":
		key := r.Header.Get("Idempotence-Key")
		if id, ok := f.idem[key]; ok {
			_ = json.NewEncoder(w).Encode(f.payments[id])
			return
		}
		var in struct {
			Amount   ykAmount          `json:"amount"`
			Metadata map[string]string `json:"metadata"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.n++
		p := &ykPayment{ID: "yk-" + strconv.Itoa(f.n), Status: "pending", Amount: in.Amount, Metadata: in.Metadata}
		p.Confirmation.URL = "https://yoomoney.ru/checkout/" + p.ID
		f.payments[p.ID], f.idem[key] = p, p.ID
		_ = json.NewEncoder(w).Encode(p)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/payments/"):
		p, ok := f.payments[strings.TrimPrefix(r.URL.Path, "/payments/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"error","code":"not_found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(p)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeYooKassa) set(id string, fn func(p *ykPayment)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.payments[id])
}

type fakeCryptoBot struct {
	mu       sync.Mutex
	invoices map[int64]*cbInvoice
	n        int64
}

func (f *fakeCryptoBot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Crypto-Pay-API-Token") != cbToken {
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":401,"name":"UNAUTHORIZED"}}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var in map[string]any
	_ = json.NewDecoder(r.Body).Decode(&in)
	var res any
	switch r.URL.Path {
	case "/createInvoice":
		f.n++
		inv := &cbInvoice{ID: f.n, Status: "active", Fiat: in["fiat"].(string), Amount: flexNumber(in["amount"].(string)), Payload: in["payload"].(string),
			BotURL: "https://t.me/CryptoBot?start=IV" + strconv.FormatInt(f.n, 10)}
		f.invoices[inv.ID] = inv
		res = inv
	case "/getInvoices":
		id, _ := strconv.ParseInt(in["invoice_ids"].(string), 10, 64)
		items := []*cbInvoice{}
		if inv, ok := f.invoices[id]; ok {
			items = append(items, inv)
		}
		res = map[string]any{"items": items}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res})
}

type env struct {
	t     *testing.T
	st    *store.Store
	s     *Service
	tg    *fakeTG
	yk    *fakeYooKassa
	cb    *fakeCryptoBot
	now   time.Time
	logs  *bytes.Buffer
	sale  db.Tariff
	token string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{t: t, now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), logs: &bytes.Buffer{}, tg: &fakeTG{},
		yk: &fakeYooKassa{payments: map[string]*ykPayment{}, idem: map[string]string{}}, cb: &fakeCryptoBot{invoices: map[int64]*cbInvoice{}}}
	var err error
	if e.st, err = store.Open(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.st.Close() })
	if err := domain.Seed(ctx, e.st, e.now); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return e.now }
	set := settings.New(e.st.Q)
	ykSrv, cbSrv := httptest.NewServer(e.yk), httptest.NewServer(e.cb)
	t.Cleanup(ykSrv.Close)
	t.Cleanup(cbSrv.Close)
	e.s = New(Deps{Store: e.st, Settings: set, Users: domain.NewUsers(e.st, domain.NewPool(e.st, clock), noChanges{}, clock),
		Log: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})), Now: clock,
		YooKassaAPI: ykSrv.URL, CryptoBotAPI: cbSrv.URL, CryptoBotTestAPI: cbSrv.URL})
	e.s.SetTelegram(e.tg)
	must(t, settings.Set(ctx, set, KeyConfig, Config{Enabled: true, Stars: true, YooKassa: true, ShopID: shopID, CryptoBot: true, AllowNew: true, RenewResetsTraffic: true}))
	must(t, settings.Set(ctx, set, KeyYooKassaSecret, ykSecret))
	must(t, settings.Set(ctx, set, KeyCryptoBotToken, cbToken))
	ts, _ := e.st.Q.ListTariffs(ctx)
	std := ts[1]
	e.sale, err = e.st.Q.UpdateTariff(ctx, db.UpdateTariffParams{Name: std.Name, TrafficLimit: std.TrafficLimit, DurationDays: 30, DeviceLimit: std.DeviceLimit,
		ResetStrategy: std.ResetStrategy, Sort: std.Sort, PriceStars: sql.NullInt64{Int64: 150, Valid: true}, PriceRub: sql.NullInt64{Int64: 19900, Valid: true}, OnSale: 1, ID: std.ID})
	must(t, err)
	_ = e.st.Q.UpsertTgChat(ctx, db.UpsertTgChatParams{TgID: 555, Username: "buyer", CreatedAt: e.now.Unix(), UpdatedAt: e.now.Unix()})
	e.token, err = e.s.WebhookToken(ctx)
	must(t, err)
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) invoice(tg, user int64, provider string) db.Payment {
	e.t.Helper()
	p, err := e.s.Invoice(context.Background(), InvoiceRequest{TgID: tg, UserID: user, TariffID: e.sale.ID, Provider: provider})
	if err != nil {
		e.t.Fatalf("invoice %s: %v", provider, err)
	}
	if p.PayUrl == "" || p.Status != "pending" {
		e.t.Fatalf("invoice %s: %+v", provider, p)
	}
	return p
}

func (e *env) payment(id int64) db.Payment {
	e.t.Helper()
	p, err := e.st.Q.GetPayment(context.Background(), id)
	must(e.t, err)
	return p
}

func (e *env) users() int {
	var n int
	_ = e.st.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM users").Scan(&n)
	return n
}

func (e *env) hook(provider, token, ip string, body []byte, hdr map[string]string) int {
	r := httptest.NewRequest(http.MethodPost, "/"+provider+"/"+token, bytes.NewReader(body))
	r.RemoteAddr = ip + ":40000"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.s.Webhook().ServeHTTP(w, r)
	return w.Code
}

func sign(token string, body []byte) string {
	key := sha256.Sum256([]byte(token))
	m := hmac.New(sha256.New, key[:])
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// A new buyer pays in Stars: one subscription, linked to the account, told once — however
// many times or how concurrently Telegram reports the payment.
func TestStarsNewSubscription(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	before := e.users()
	p := e.invoice(555, 0, Stars)
	if p.Amount != 150 || p.Currency != "XTR" || p.Kind != "new" {
		t.Fatalf("invoice: %+v", p)
	}
	// Asking again within minutes shows the same invoice.
	if again := e.invoice(555, 0, Stars); again.ID != p.ID {
		t.Fatalf("a second invoice for the same purchase: %d", again.ID)
	}
	for _, c := range []struct {
		name         string
		tg           int64
		payload, cur string
		amount       int64
	}{
		{"other account", 556, p.Payload, "XTR", 150},
		{"other amount", 555, p.Payload, "XTR", 1},
		{"other currency", 555, p.Payload, "USD", 150},
		{"unknown payload", 555, "nope", "XTR", 150},
	} {
		if err := e.s.PreCheckout(ctx, c.tg, c.payload, c.cur, c.amount); err == nil {
			t.Errorf("pre-checkout %s accepted", c.name)
		}
		if err := e.s.StarsPaid(ctx, c.tg, c.payload, "ch-x", c.cur, c.amount); err == nil {
			t.Errorf("payment %s accepted", c.name)
		}
	}
	must(t, e.s.PreCheckout(ctx, 555, p.Payload, "XTR", 150))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.s.StarsPaid(ctx, 555, p.Payload, "ch-1", "XTR", 150)
		}()
	}
	wg.Wait()
	// A different charge id for the same invoice changes nothing either.
	_ = e.s.StarsPaid(ctx, 555, p.Payload, "ch-2", "XTR", 150)
	got := e.payment(p.ID)
	if got.Status != "applied" || got.ExternalID.String != "ch-1" || !got.UserID.Valid {
		t.Fatalf("payment: %+v", got)
	}
	if e.users() != before+1 || e.tg.told() != 1 {
		t.Fatalf("users %d (was %d), told %d", e.users(), before, e.tg.told())
	}
	link, err := e.st.Q.GetTgLink(ctx, got.UserID.Int64)
	if err != nil || link.TgID != 555 {
		t.Fatalf("link: %+v %v", link, err)
	}
	u, _ := e.st.Q.GetUser(ctx, got.UserID.Int64)
	if u.Name != "@buyer" || time.Unix(u.ExpiresAt.Int64, 0).Sub(e.now) != 30*24*time.Hour {
		t.Fatalf("user: %+v", u)
	}
	// Paid once: the invoice cannot be paid again.
	if err := e.s.PreCheckout(ctx, 555, p.Payload, "XTR", 150); err == nil {
		t.Fatal("an applied invoice passed pre-checkout")
	}
	// Refund: Stars go back through the bot, the payment says so.
	must(t, e.s.Refund(ctx, p.ID))
	if e.payment(p.ID).Status != "refunded" || len(e.tg.refunds) != 1 || e.tg.refunds[0] != "ch-1" {
		t.Fatalf("refund: %+v %v", e.payment(p.ID), e.tg.refunds)
	}
	if err := e.s.Refund(ctx, p.ID); !errors.Is(err, ErrNotRefunable) {
		t.Fatalf("second refund: %v", err)
	}
}

// Renewal: only the owner renews, the term goes on from the current end, the traffic
// period starts anew.
func TestRenewal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	clock := func() time.Time { return e.now }
	u, err := domain.NewUsers(e.st, domain.NewPool(e.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: e.sale.ID})
	must(t, err)
	must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 555, CreatedAt: e.now.Unix()}))
	_, err = e.st.DB.ExecContext(ctx, "UPDATE users SET used_up = 1000, used_down = 2000 WHERE id = ?", u.ID)
	must(t, err)

	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 777, UserID: u.ID, TariffID: e.sale.ID, Provider: Stars}); !errors.Is(err, ErrNotYours) {
		t.Fatalf("someone else's subscription: %v", err)
	}
	p := e.invoice(555, u.ID, Stars)
	must(t, e.s.StarsPaid(ctx, 555, p.Payload, "ch-r", "XTR", 150))
	after, _ := e.st.Q.GetUser(ctx, u.ID)
	if after.ExpiresAt.Int64 != u.ExpiresAt.Int64+30*24*3600 || after.UsedUp+after.UsedDown != 0 || after.Status != "active" {
		t.Fatalf("renewed: expires %d (was %d), used %d", after.ExpiresAt.Int64, u.ExpiresAt.Int64, after.UsedUp+after.UsedDown)
	}
	if e.payment(p.ID).UserID.Int64 != u.ID {
		t.Fatal("payment not tied to the renewed user")
	}
}

func TestInvoiceRefusals(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ts, _ := e.st.Q.ListTariffs(ctx)
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: ts[0].ID, Provider: Stars}); !errors.Is(err, ErrNotForSale) {
		t.Fatalf("not on sale: %v", err)
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: e.sale.ID, Provider: "paypal"}); !errors.Is(err, ErrProviderOff) {
		t.Fatalf("unknown provider: %v", err)
	}
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{Enabled: true, Stars: true, AllowNew: false}))
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: e.sale.ID, Provider: YooKassa}); !errors.Is(err, ErrProviderOff) {
		t.Fatalf("yookassa off: %v", err)
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: e.sale.ID, Provider: Stars}); !errors.Is(err, ErrNewOff) {
		t.Fatalf("new buyers off: %v", err)
	}
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{Enabled: true, Stars: true, AllowNew: true}))
	for i := range 5 {
		u, err := e.s.d.Users.Create(ctx, domain.CreateInput{Name: "x" + strconv.Itoa(i), TariffID: e.sale.ID})
		must(t, err)
		must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 900, CreatedAt: 1}))
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 900, TariffID: e.sale.ID, Provider: Stars}); err == nil {
		t.Fatal("a sixth subscription for one account")
	}
	// One account cannot open invoices without end.
	for tariff := range maxPerHour + 1 {
		_, err := e.st.Q.CreatePayment(ctx, db.CreatePaymentParams{Provider: Stars, Payload: "p" + strconv.Itoa(tariff), TgID: 321, Kind: "new",
			TariffID: sql.NullInt64{Int64: e.sale.ID, Valid: true}, TariffName: "x", Amount: 1, Currency: "XTR", CreatedAt: e.now.Unix()})
		must(t, err)
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 321, TariffID: e.sale.ID, Provider: YooKassa}); !errors.Is(err, ErrTooMany) && !errors.Is(err, ErrProviderOff) {
		t.Fatalf("invoice flood: %v", err)
	}
}

// YooKassa: notifications only from its addresses, and only what its API confirms counts.
func TestYooKassa(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.invoice(555, 0, YooKassa)
	if p.Amount != 19900 || p.Currency != "RUB" || !strings.HasPrefix(p.PayUrl, "https://yoomoney.ru/") {
		t.Fatalf("invoice: %+v", p)
	}
	id := p.ExternalID.String
	note := []byte(`{"type":"notification","event":"payment.succeeded","object":{"id":"` + id + `","status":"succeeded","paid":true,"amount":{"value":"199.00","currency":"RUB"}}}`)
	const ykIP = "185.71.76.5"

	if code := e.hook(YooKassa, "wrong-token-wrong-token-wrong-tok", ykIP, note, nil); code != http.StatusNotFound {
		t.Fatalf("wrong token: %d", code)
	}
	if code := e.hook(YooKassa, e.token, "203.0.113.9", note, nil); code != http.StatusForbidden {
		t.Fatalf("foreign address: %d", code)
	}
	// The notification claims success, the API says it is still pending: nothing happens.
	if code := e.hook(YooKassa, e.token, ykIP, note, nil); code != http.StatusOK || e.payment(p.ID).Status != "pending" {
		t.Fatalf("unconfirmed: %d %s", code, e.payment(p.ID).Status)
	}
	// Paid, but less than the invoice: not applied.
	e.yk.set(id, func(y *ykPayment) { y.Status, y.Paid, y.Amount.Value = "succeeded", true, "1.00" })
	e.hook(YooKassa, e.token, ykIP, note, nil)
	if e.payment(p.ID).Status != "pending" {
		t.Fatal("a smaller amount was accepted")
	}
	e.yk.set(id, func(y *ykPayment) { y.Amount.Value = "199.00" })
	before := e.users()
	for range 3 {
		if code := e.hook(YooKassa, e.token, ykIP, note, nil); code != http.StatusOK {
			t.Fatalf("webhook: %d", code)
		}
	}
	if e.payment(p.ID).Status != "applied" || e.users() != before+1 || e.tg.told() != 1 {
		t.Fatalf("applied: %s users %d→%d told %d", e.payment(p.ID).Status, before, e.users(), e.tg.told())
	}
	// A notification about a payment of someone else's shop is ignored.
	if code := e.hook(YooKassa, e.token, ykIP, []byte(`{"event":"payment.succeeded","object":{"id":"yk-999"}}`), nil); code != http.StatusOK {
		t.Fatalf("unknown payment: %d", code)
	}

	// Without a webhook the reconcile pass finds the payment.
	q := e.invoice(555, 0, YooKassa)
	e.yk.set(q.ExternalID.String, func(y *ykPayment) { y.Status, y.Paid = "succeeded", true })
	e.s.Reconcile(ctx)
	if e.payment(q.ID).Status != "applied" {
		t.Fatalf("reconcile: %s", e.payment(q.ID).Status)
	}
	// Canceled payments fail; old unpaid invoices expire.
	c := e.invoice(556, 0, YooKassa)
	e.yk.set(c.ExternalID.String, func(y *ykPayment) { y.Status = "canceled" })
	e.s.Reconcile(ctx)
	if e.payment(c.ID).Status != "failed" {
		t.Fatalf("canceled: %s", e.payment(c.ID).Status)
	}
	old := e.invoice(557, 0, Stars)
	e.now = e.now.Add(25 * time.Hour)
	e.s.Reconcile(ctx)
	if e.payment(old.ID).Status != "expired" {
		t.Fatalf("expired: %s", e.payment(old.ID).Status)
	}
	if strings.Contains(e.logs.String(), ykSecret) {
		t.Fatal("the shop's secret key is in the log")
	}
}

// CryptoBot: the signature is checked, then the API is asked.
func TestCryptoBot(t *testing.T) {
	e := newEnv(t)
	p := e.invoice(555, 0, CryptoBot)
	if p.Amount != 19900 || !strings.HasPrefix(p.PayUrl, "https://t.me/CryptoBot") {
		t.Fatalf("invoice: %+v", p)
	}
	id, _ := strconv.ParseInt(p.ExternalID.String, 10, 64)
	body, _ := json.Marshal(map[string]any{"update_type": "invoice_paid", "payload": map[string]any{"invoice_id": id, "status": "paid", "fiat": "RUB", "amount": "199.00", "payload": p.Payload}})
	hdr := func(sig string) map[string]string { return map[string]string{"Crypto-Pay-Api-Signature": sig} }

	for _, sig := range []string{"", "00", sign("other-token", body), strings.Repeat("z", 64)} {
		if code := e.hook(CryptoBot, e.token, "203.0.113.1", body, hdr(sig)); code != http.StatusForbidden {
			t.Fatalf("signature %q: %d", sig, code)
		}
	}
	// Signed, but the API says the invoice is not paid: nothing happens.
	if code := e.hook(CryptoBot, e.token, "203.0.113.1", body, hdr(sign(cbToken, body))); code != http.StatusOK || e.payment(p.ID).Status != "pending" {
		t.Fatalf("unpaid: %d %s", code, e.payment(p.ID).Status)
	}
	e.cb.mu.Lock()
	e.cb.invoices[id].Status = "paid"
	e.cb.mu.Unlock()
	before := e.users()
	for range 3 {
		e.hook(CryptoBot, e.token, "203.0.113.1", body, hdr(sign(cbToken, body)))
	}
	if e.payment(p.ID).Status != "applied" || e.users() != before+1 || e.tg.told() != 1 {
		t.Fatalf("applied: %s", e.payment(p.ID).Status)
	}
	// A body changed after signing is refused.
	forged := bytes.Replace(body, []byte(`199.00`), []byte(`1.00`), 1)
	if code := e.hook(CryptoBot, e.token, "203.0.113.1", forged, hdr(sign(cbToken, body))); code != http.StatusForbidden {
		t.Fatalf("forged body: %d", code)
	}
	if strings.Contains(e.logs.String(), cbToken) {
		t.Fatal("the CryptoBot token is in the log")
	}
}

func TestMoney(t *testing.T) {
	for s, want := range map[string]int64{"199.00": 19900, "199": 19900, "0.5": 50, "1.05": 105, "1.005": -1, "-1": -1, "x": -1} {
		if got := kopecks(s); got != want {
			t.Errorf("%q: %d, want %d", s, got, want)
		}
	}
	if rubles(19905) != "199.05" {
		t.Error(rubles(19905))
	}
}

// With the reset off a renewal only adds the term: the traffic counter runs on.
func TestRenewalKeepsTraffic(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{Enabled: true, Stars: true, AllowNew: true, RenewResetsTraffic: false}))
	clock := func() time.Time { return e.now }
	u, err := domain.NewUsers(e.st, domain.NewPool(e.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: e.sale.ID})
	must(t, err)
	must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 555, CreatedAt: e.now.Unix()}))
	_, err = e.st.DB.ExecContext(ctx, "UPDATE users SET used_up = 1000, used_down = 2000 WHERE id = ?", u.ID)
	must(t, err)
	p := e.invoice(555, u.ID, Stars)
	must(t, e.s.StarsPaid(ctx, 555, p.Payload, "ch-k", "XTR", 150))
	after, _ := e.st.Q.GetUser(ctx, u.ID)
	if after.ExpiresAt.Int64 != u.ExpiresAt.Int64+30*24*3600 || after.UsedUp+after.UsedDown != 3000 {
		t.Fatalf("renewed: expires %d, used %d", after.ExpiresAt.Int64, after.UsedUp+after.UsedDown)
	}
}

// CryptoBot documents amounts as strings; a number is taken too.
func TestCryptoAmountForms(t *testing.T) {
	for raw, want := range map[string]int64{`{"amount":"199.00"}`: 19900, `{"amount":199}`: 19900, `{"amount":199.5}`: 19950} {
		var inv cbInvoice
		if err := json.Unmarshal([]byte(raw), &inv); err != nil || kopecks(string(inv.Amount)) != want {
			t.Errorf("%s: %q %v", raw, inv.Amount, err)
		}
	}
}

// YooKassa's refusal names the field, so "receipt" shows when the shop wants 54-FZ receipts.
func TestYooKassaErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","code":"invalid_request","parameter":"receipt","description":"Receipt is missing or illegal"}`))
	}))
	t.Cleanup(srv.Close)
	_, _, err := yooKassa{base: srv.URL, shopID: "1", secret: "s", hc: srv.Client()}.create(context.Background(), "p", 100, "x", "")
	if errCode(err) != "yookassa_invalid_request:receipt" {
		t.Fatalf("code: %q", errCode(err))
	}
}

// Selling off: nothing on offer and no new invoices, but an invoice opened before still
// turns into the subscription once it is paid — nobody pays for nothing.
func TestSalesOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.invoice(555, 0, Stars)
	c := e.s.Config(ctx)
	c.Enabled = false
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, c))

	if o, av, err := e.s.Offers(ctx); err != nil || len(o) != 0 || av.Any() {
		t.Fatalf("offers with selling off: %v %+v %v", o, av, err)
	}
	for _, prov := range []string{Stars, YooKassa, CryptoBot} {
		if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 556, TariffID: e.sale.ID, Provider: prov}); !errors.Is(err, ErrProviderOff) {
			t.Fatalf("invoice %s with selling off: %v", prov, err)
		}
	}
	must(t, e.s.PreCheckout(ctx, 555, p.Payload, "XTR", 150))
	must(t, e.s.StarsPaid(ctx, 555, p.Payload, "ch-off", "XTR", 150))
	if got := e.payment(p.ID); got.Status != "applied" || !got.UserID.Valid {
		t.Fatalf("an invoice opened before selling went off: %+v", got)
	}
}

// Payment settings saved before the switch existed keep selling; a panel that never saved
// them starts with selling off.
func TestSalesDefault(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.st.DB.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", KeyConfig)
	must(t, err)
	if e.s.Config(ctx).Enabled || e.s.Available(ctx).Any() {
		t.Fatal("selling on in a panel that never set it up")
	}
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, map[string]any{"stars": true, "allow_new": true}))
	if c := e.s.Config(ctx); !c.Enabled || !c.Stars {
		t.Fatalf("settings from 0.4.1 lost selling: %+v", c)
	}
}
