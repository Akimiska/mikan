// Package billing sells tariffs: the bot and the Mini App offer the tariffs on sale, a
// payment through Telegram Stars, YooKassa or CryptoBot creates or renews the buyer's
// subscription, and the buyer gets the link.
//
// A payment row is made before the buyer pays, with an unguessable payload that every
// provider echoes back. Money is trusted only from the provider itself: Telegram's
// successful_payment on the bot's own long poll, YooKassa's API asked again with the
// shop's key (its webhooks are not signed), CryptoBot's HMAC-signed webhook confirmed by
// its API. The provider's payment id is unique per provider and a payment moves from
// paid to applied once, on the same transaction that changes the subscription, so a
// repeated or concurrent notification never pays out twice.
package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Providers.
const (
	Stars     = "stars"
	YooKassa  = "yookassa"
	CryptoBot = "cryptobot"
)

// Settings keys. The secrets never leave the panel's API.
const (
	KeyConfig          = "pay_config"
	KeyYooKassaSecret  = "pay_yookassa_secret"
	KeyCryptoBotToken  = "pay_cryptobot_token"
	KeyWebhookToken    = "pay_webhook_token" // the secret part of the webhook URLs
	invoiceReuse       = 10 * time.Minute    // an open invoice for the same purchase is shown again
	pendingTTL         = 24 * time.Hour      // unpaid invoices expire
	reconcileEvery     = time.Minute
	maxPerHour         = 20 // invoices one Telegram account may open in an hour
	cryptoInvoiceTTL   = time.Hour
	providerHTTPTimout = 15 * time.Second
)

// Config is what the admin sets; secrets are separate settings.
type Config struct {
	// Enabled is the switch for selling at all: off, the bot and the Mini App offer
	// nothing and take no new invoices, while invoices already opened are still applied.
	Enabled   bool   `json:"enabled"`
	Stars     bool   `json:"stars"`
	YooKassa  bool   `json:"yookassa"`
	ShopID    string `json:"yookassa_shop_id"`
	CryptoBot bool   `json:"cryptobot"`
	Testnet   bool   `json:"cryptobot_testnet"`
	// AllowNew lets people without a subscription buy one; off: only renewals.
	AllowNew bool `json:"allow_new"`
	// RenewResetsTraffic: a paid renewal also starts a new traffic period; off, the
	// counter keeps running and only the term is extended.
	RenewResetsTraffic bool `json:"renew_resets_traffic"`
}

// DefaultConfig: selling off until the admin turns it on; then Stars (it needs nothing but
// the bot) and new buyers are welcome.
var DefaultConfig = Config{Stars: true, AllowNew: true, RenewResetsTraffic: true}

// Telegram is the bot's part: Stars invoices and refunds, and telling buyers.
type Telegram interface {
	// InvoiceLink makes a Stars invoice link (createInvoiceLink); "" when the bot is off.
	InvoiceLink(ctx context.Context, title, description, payload string, stars int64) (string, error)
	RefundStars(ctx context.Context, tgID int64, chargeID string) error
	// Paid tells the buyer the subscription is ready.
	Paid(ctx context.Context, p db.Payment, u db.User, created bool)
	// BotURL is https://t.me/<bot>, "" while the bot is off.
	BotURL(ctx context.Context) string
}

type Deps struct {
	Store    *store.Store
	Settings *settings.Settings
	Users    *domain.Users
	Log      *slog.Logger
	Now      func() time.Time
	HTTP     *http.Client // nil: a client with a timeout
	// Provider APIs; empty: the real ones (tests point them at fakes).
	YooKassaAPI, CryptoBotAPI, CryptoBotTestAPI string
	// TrustProxy reads the client's IP from X-Forwarded-For (the YooKassa IP check).
	TrustProxy bool
	MaxLinks   int64 // subscriptions one Telegram account may hold
}

type Service struct {
	d  Deps
	mu sync.Mutex
	tg Telegram
}

func New(d Deps) *Service {
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: providerHTTPTimout}
	}
	if d.YooKassaAPI == "" {
		d.YooKassaAPI = "https://api.yookassa.ru/v3"
	}
	if d.CryptoBotAPI == "" {
		d.CryptoBotAPI = "https://pay.crypt.bot/api"
	}
	if d.CryptoBotTestAPI == "" {
		d.CryptoBotTestAPI = "https://testnet-pay.crypt.bot/api"
	}
	if d.MaxLinks == 0 {
		d.MaxLinks = 5
	}
	return &Service{d: d}
}

// SetTelegram connects the bot; until then Stars are off.
func (s *Service) SetTelegram(tg Telegram) {
	s.mu.Lock()
	s.tg = tg
	s.mu.Unlock()
}

func (s *Service) telegram() Telegram {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tg
}

// Errors the bot and the Mini App show to buyers.
var (
	ErrNotForSale   = errors.New("not_for_sale")
	ErrProviderOff  = errors.New("provider_off")
	ErrNotYours     = errors.New("not_yours")
	ErrNewOff       = errors.New("new_off")
	ErrTooManySubs  = errors.New("too_many_subs")
	ErrTooMany      = errors.New("too_many_invoices")
	ErrBadPayment   = errors.New("bad_payment")
	ErrNotRefunable = errors.New("not_refundable")
)

// LoadConfig reads the payment settings. A read error is returned, never replaced by the
// defaults: settings changed on top of those and saved would switch selling off and
// forget the providers.
func (s *Service) LoadConfig(ctx context.Context) (Config, error) {
	// Payment settings saved before the switch existed (0.4.0, 0.4.1) come from panels that
	// set up selling: they keep selling. A panel that never saved them starts with it off.
	saved := DefaultConfig
	saved.Enabled = true
	c, found, err := settings.GetOver(ctx, s.d.Settings, KeyConfig, saved)
	switch {
	case err != nil:
		return DefaultConfig, err
	case !found:
		return DefaultConfig, nil
	}
	return c, nil
}

// Config is what decides what is on offer. When the settings cannot be read nothing is:
// selling fails closed until the store answers again.
func (s *Service) Config(ctx context.Context) Config {
	c, err := s.LoadConfig(ctx)
	if err != nil {
		s.d.Log.Warn("billing: payment settings unreadable, selling paused", "err", err)
		c.Enabled = false
	}
	return c
}

// Secrets holds what the providers need besides Config.
type Secrets struct{ YooKassaSecret, CryptoBotToken string }

func (s *Service) secrets(ctx context.Context) Secrets {
	var sec Secrets
	sec.YooKassaSecret, _ = s.d.Settings.String(ctx, KeyYooKassaSecret)
	sec.CryptoBotToken, _ = s.d.Settings.String(ctx, KeyCryptoBotToken)
	return sec
}

// Available says which providers can take a payment right now: selling on, the provider
// on, configured and, for Stars, with the bot running.
type Available struct {
	Stars, YooKassa, CryptoBot bool
}

func (a Available) Any() bool { return a.Stars || a.YooKassa || a.CryptoBot }

func (s *Service) Available(ctx context.Context) Available {
	c, sec := s.Config(ctx), s.secrets(ctx)
	if !c.Enabled {
		return Available{}
	}
	tg := s.telegram()
	return Available{
		Stars:     c.Stars && tg != nil && tg.BotURL(ctx) != "",
		YooKassa:  c.YooKassa && c.ShopID != "" && sec.YooKassaSecret != "",
		CryptoBot: c.CryptoBot && sec.CryptoBotToken != "",
	}
}

// Offer is a tariff on sale with the prices the available providers take.
type Offer struct {
	Tariff db.Tariff
	Stars  int64 // 0: not for Stars
	Rub    int64 // kopecks; 0: not for rubles
}

// Offers lists the tariffs a buyer can pay for now.
func (s *Service) Offers(ctx context.Context) ([]Offer, Available, error) {
	av := s.Available(ctx)
	if !av.Any() {
		return nil, av, nil
	}
	ts, err := s.d.Store.Q.ListTariffsOnSale(ctx)
	if err != nil {
		return nil, av, err
	}
	var out []Offer
	for _, t := range ts {
		o := Offer{Tariff: t}
		if av.Stars && t.PriceStars.Valid {
			o.Stars = t.PriceStars.Int64
		}
		if (av.YooKassa || av.CryptoBot) && t.PriceRub.Valid {
			o.Rub = t.PriceRub.Int64
		}
		if o.Stars > 0 || o.Rub > 0 {
			out = append(out, o)
		}
	}
	return out, av, nil
}

// InvoiceRequest: who buys which tariff with what. UserID 0 buys a new subscription.
type InvoiceRequest struct {
	TgID     int64
	UserID   int64
	TariffID int64
	Provider string
}

// Invoice opens a payment and returns it with the URL to pay at. An open invoice for the
// same purchase made in the last minutes is returned again instead of a new one.
func (s *Service) Invoice(ctx context.Context, req InvoiceRequest) (db.Payment, error) {
	q := s.d.Store.Q
	t, err := q.GetTariff(ctx, req.TariffID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (t.Archived != 0 || t.OnSale == 0) {
		return db.Payment{}, ErrNotForSale
	}
	if err != nil {
		return db.Payment{}, err
	}
	av := s.Available(ctx)
	var amount int64
	var currency string
	switch {
	case req.Provider == Stars && av.Stars && t.PriceStars.Valid:
		amount, currency = t.PriceStars.Int64, "XTR"
	case (req.Provider == YooKassa && av.YooKassa || req.Provider == CryptoBot && av.CryptoBot) && t.PriceRub.Valid:
		amount, currency = t.PriceRub.Int64, "RUB"
	default:
		return db.Payment{}, ErrProviderOff
	}
	kind := "renew"
	if req.UserID == 0 {
		kind = "new"
		if !s.Config(ctx).AllowNew {
			return db.Payment{}, ErrNewOff
		}
		if n, err := q.CountTgLinksOf(ctx, req.TgID); err != nil {
			return db.Payment{}, err
		} else if n >= s.d.MaxLinks {
			return db.Payment{}, ErrTooManySubs
		}
	} else if link, err := q.GetTgLink(ctx, req.UserID); err != nil || link.TgID != req.TgID {
		// Only the subscription's owner renews it through the bot.
		return db.Payment{}, ErrNotYours
	}
	now := s.d.Now()
	userID := sql.NullInt64{Int64: req.UserID, Valid: req.UserID != 0}
	if p, err := q.FindOpenPayment(ctx, db.FindOpenPaymentParams{TgID: req.TgID, TariffID: t.ID, Provider: req.Provider, Kind: kind,
		UserID: sql.NullInt64{Int64: req.UserID, Valid: true}, Since: now.Add(-invoiceReuse).Unix()}); err == nil && p.Amount == amount {
		return p, nil
	}
	if n, err := s.recentInvoices(ctx, req.TgID, now); err != nil {
		return db.Payment{}, err
	} else if n >= maxPerHour {
		return db.Payment{}, ErrTooMany
	}
	p, err := q.CreatePayment(ctx, db.CreatePaymentParams{Provider: req.Provider, Payload: secure.Token(32), TgID: req.TgID, Kind: kind, UserID: userID,
		TariffID: t.ID, TariffName: t.Name, Amount: amount, Currency: currency, CreatedAt: now.Unix()})
	if err != nil {
		return db.Payment{}, err
	}
	ext, url, err := s.openInvoice(ctx, p, t)
	if err != nil {
		_, _ = q.SetPaymentStatus(ctx, db.SetPaymentStatusParams{NewStatus: "failed", ID: p.ID, OldStatus: "pending"})
		_ = q.SetPaymentError(ctx, db.SetPaymentErrorParams{Error: errCode(err), ID: p.ID})
		s.d.Log.Warn("billing: invoice", "provider", p.Provider, "payment", p.ID, "err", err)
		return db.Payment{}, fmt.Errorf("%w: %s", ErrProviderOff, errCode(err))
	}
	if err := q.SetPaymentInvoice(ctx, db.SetPaymentInvoiceParams{ExternalID: ext, PayUrl: url, ID: p.ID}); err != nil {
		return db.Payment{}, err
	}
	p.ExternalID, p.PayUrl = ext, url
	return p, nil
}

func (s *Service) recentInvoices(ctx context.Context, tgID int64, now time.Time) (int, error) {
	open, err := s.d.Store.Q.ListOpenPayments(ctx, now.Add(-time.Hour).Unix())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range open {
		if p.TgID == tgID {
			n++
		}
	}
	return n, nil
}

// openInvoice asks the provider for the invoice: its id (none for Stars until paid) and
// the URL the buyer pays at.
func (s *Service) openInvoice(ctx context.Context, p db.Payment, t db.Tariff) (sql.NullString, string, error) {
	title := t.Name
	lang, _ := s.d.Settings.Lang(ctx)
	desc := Describe(t, lang)
	switch p.Provider {
	case Stars:
		tg := s.telegram()
		if tg == nil {
			return sql.NullString{}, "", ErrProviderOff
		}
		url, err := tg.InvoiceLink(ctx, title, desc, p.Payload, p.Amount)
		return sql.NullString{}, url, err
	case YooKassa:
		c, sec := s.Config(ctx), s.secrets(ctx)
		y := yooKassa{base: s.d.YooKassaAPI, shopID: c.ShopID, secret: sec.YooKassaSecret, hc: s.d.HTTP}
		ret := ""
		if tg := s.telegram(); tg != nil {
			ret = tg.BotURL(ctx)
		}
		id, url, err := y.create(ctx, p.Payload, p.Amount, title+" — "+desc, ret)
		return sql.NullString{String: id, Valid: id != ""}, url, err
	case CryptoBot:
		cb := s.cryptoBot(ctx)
		back := ""
		if tg := s.telegram(); tg != nil {
			back = tg.BotURL(ctx)
		}
		id, url, err := cb.create(ctx, p.Payload, p.Amount, title+" — "+desc, back)
		return sql.NullString{String: id, Valid: id != ""}, url, err
	}
	return sql.NullString{}, "", ErrProviderOff
}

func (s *Service) cryptoBot(ctx context.Context) cryptoBot {
	base := s.d.CryptoBotAPI
	if s.Config(ctx).Testnet {
		base = s.d.CryptoBotTestAPI
	}
	return cryptoBot{base: base, token: s.secrets(ctx).CryptoBotToken, hc: s.d.HTTP}
}

// Describe is a tariff in a line, "30 days · 100 GB · 3 devices", in lang ("en", else
// Russian): invoices, the bot and the Mini App show it.
func Describe(t db.Tariff, lang string) string {
	en := lang == "en"
	pick := func(ru, en_ string) string {
		if en {
			return en_
		}
		return ru
	}
	parts := []string{}
	if t.DurationDays > 0 {
		parts = append(parts, fmt.Sprintf(pick("%d дн.", "%d days"), t.DurationDays))
	} else {
		parts = append(parts, pick("бессрочно", "no end date"))
	}
	if t.TrafficLimit.Valid {
		parts = append(parts, fmt.Sprintf(pick("%d ГБ", "%d GB"), t.TrafficLimit.Int64>>30))
	} else {
		parts = append(parts, pick("трафик без лимита", "unlimited traffic"))
	}
	if t.DeviceLimit.Valid {
		parts = append(parts, fmt.Sprintf(pick("устройств: %d", "devices: %d"), t.DeviceLimit.Int64))
	}
	return strings.Join(parts, " · ")
}

// PreCheckout is Telegram asking whether a Stars payment may go ahead.
func (s *Service) PreCheckout(ctx context.Context, tgID int64, payload, currency string, amount int64) error {
	p, err := s.d.Store.Q.GetPaymentByPayload(ctx, payload)
	if err != nil || p.Provider != Stars || p.Status != "pending" || p.TgID != tgID || p.Currency != currency || p.Amount != amount {
		return ErrBadPayment
	}
	t, err := s.d.Store.Q.GetTariff(ctx, p.TariffID)
	if err != nil || t.Archived != 0 || t.OnSale == 0 {
		return ErrNotForSale
	}
	return nil
}

// StarsPaid records a successful_payment from the bot's own update stream and applies it.
func (s *Service) StarsPaid(ctx context.Context, tgID int64, payload, chargeID, currency string, amount int64) error {
	p, err := s.d.Store.Q.GetPaymentByPayload(ctx, payload)
	if err != nil || p.Provider != Stars || p.TgID != tgID || p.Currency != currency || p.Amount != amount || chargeID == "" {
		s.d.Log.Error("billing: stars payment does not match an invoice", "tg", tgID, "amount", amount)
		return ErrBadPayment
	}
	return s.paid(ctx, p, chargeID)
}

// paid marks the payment paid (once) and applies it.
func (s *Service) paid(ctx context.Context, p db.Payment, externalID string) error {
	n, err := s.d.Store.Q.MarkPaymentPaid(ctx, db.MarkPaymentPaidParams{ExternalID: sql.NullString{String: externalID, Valid: true},
		PaidAt: sql.NullInt64{Int64: s.d.Now().Unix(), Valid: true}, ID: p.ID})
	if err != nil {
		return err
	}
	if n == 1 {
		s.d.Log.Info("billing: paid", "payment", p.ID, "provider", p.Provider, "amount", p.Amount, "currency", p.Currency)
	}
	return s.Apply(ctx, p.ID)
}

// Apply turns a paid payment into the subscription. It runs once per payment: the status
// moves to applied on the transaction that creates or renews the user.
func (s *Service) Apply(ctx context.Context, id int64) error {
	var (
		pay     db.Payment
		u       db.User
		created bool
		done    bool
	)
	// A payment is applied with the settings as they are; unreadable, it waits for the
	// next attempt rather than guessing.
	cfg, err := s.LoadConfig(ctx)
	if err != nil {
		return err
	}
	reset := cfg.RenewResetsTraffic
	run := func(q *db.Queries) error {
		var err error
		if pay, err = q.GetPayment(ctx, id); err != nil {
			return err
		}
		if pay.Status != "paid" {
			done = true
			return nil
		}
		var userID int64
		if pay.Kind == "renew" && pay.UserID.Valid {
			userID = pay.UserID.Int64
		}
		if u, created, err = s.d.Users.Purchase(ctx, q, userID, pay.TariffID, buyerName(ctx, q, pay.TgID), reset); err != nil {
			return err
		}
		if created {
			if err := q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: pay.TgID, CreatedAt: s.d.Now().Unix()}); err != nil {
				return err
			}
			_ = q.SetTgCurrent(ctx, db.SetTgCurrentParams{Current: u.ID, TgID: pay.TgID})
		}
		n, err := q.MarkPaymentApplied(ctx, db.MarkPaymentAppliedParams{UserID: sql.NullInt64{Int64: u.ID, Valid: true},
			AppliedAt: sql.NullInt64{Int64: s.d.Now().Unix(), Valid: true}, ID: id})
		if err == nil && n != 1 {
			err = errors.New("payment changed while applying")
		}
		return err
	}
	err = s.d.Store.Tx(ctx, run)
	if errors.Is(err, domain.ErrNoSlots) {
		if err = s.d.Users.RefillSlots(ctx); err == nil {
			err = s.d.Store.Tx(ctx, run)
		}
	}
	if err != nil {
		// Paid but not applied: the reconcile pass tries again, the admin sees why.
		_ = s.d.Store.Q.SetPaymentError(ctx, db.SetPaymentErrorParams{Error: errCode(err), ID: id})
		s.d.Log.Error("billing: apply", "payment", id, "err", err)
		return err
	}
	if done {
		return nil
	}
	s.d.Users.Changed()
	pay.Status, pay.UserID = "applied", sql.NullInt64{Int64: u.ID, Valid: true}
	s.d.Log.Info("billing: applied", "payment", id, "user", u.ID, "created", created)
	if tg := s.telegram(); tg != nil {
		tg.Paid(ctx, pay, u, created)
	}
	return nil
}

// buyerName names a subscription bought by a Telegram account: its @username, its name,
// or its id.
func buyerName(ctx context.Context, q *db.Queries, tgID int64) string {
	if c, err := q.GetTgChat(ctx, tgID); err == nil {
		if c.Username != "" {
			return "@" + c.Username
		}
		if name := strings.TrimSpace(c.FirstName); name != "" {
			return name
		}
	}
	return fmt.Sprintf("tg %d", tgID)
}

// Refund returns a Stars payment to the buyer. The subscription stays as it is: the admin
// decides about it. Rubles and crypto are refunded in the provider's own dashboard.
func (s *Service) Refund(ctx context.Context, id int64) error {
	p, err := s.d.Store.Q.GetPayment(ctx, id)
	if err != nil {
		return err
	}
	if p.Provider != Stars || p.Status != "applied" || !p.ExternalID.Valid {
		return ErrNotRefunable
	}
	tg := s.telegram()
	if tg == nil {
		return ErrProviderOff
	}
	if err := tg.RefundStars(ctx, p.TgID, p.ExternalID.String); err != nil {
		return err
	}
	_, err = s.d.Store.Q.MarkPaymentRefunded(ctx, db.MarkPaymentRefundedParams{RefundedAt: sql.NullInt64{Int64: s.d.Now().Unix(), Valid: true}, ID: id})
	return err
}

// Run reconciles in the background: applies paid payments that failed to apply, asks
// YooKassa and CryptoBot about open invoices (a webhook may never come) and expires
// invoices nobody paid.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(reconcileEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Reconcile(ctx)
		}
	}
}

// Reconcile is one pass of Run.
func (s *Service) Reconcile(ctx context.Context) {
	now := s.d.Now()
	q := s.d.Store.Q
	open, err := q.ListOpenPayments(ctx, now.Add(-7*24*time.Hour).Unix())
	if err != nil {
		s.d.Log.Error("billing: reconcile", "err", err)
		return
	}
	for _, p := range open {
		if ctx.Err() != nil {
			return
		}
		switch {
		case p.Status == "paid":
			_ = s.Apply(ctx, p.ID)
		case p.Provider == YooKassa && p.ExternalID.Valid:
			_ = s.checkYooKassa(ctx, p.ExternalID.String)
		case p.Provider == CryptoBot && p.ExternalID.Valid:
			_ = s.checkCryptoBot(ctx, p.ExternalID.String)
		}
	}
	if _, err := q.ExpirePayments(ctx, now.Add(-pendingTTL).Unix()); err != nil {
		s.d.Log.Error("billing: expire", "err", err)
	}
}

// WebhookToken is the secret path part of the webhook URLs, made once.
func (s *Service) WebhookToken(ctx context.Context) (string, error) {
	tok, err := s.d.Settings.String(ctx, KeyWebhookToken)
	if err != nil || len(tok) == 32 {
		return tok, err
	}
	tok = secure.Token(32)
	return tok, settings.Set(ctx, s.d.Settings, KeyWebhookToken, tok)
}

// errCode keeps provider errors short and free of secrets for the payments list.
func errCode(err error) string {
	var pe *providerError
	switch {
	case errors.As(err, &pe):
		return pe.Code
	case errors.Is(err, domain.ErrNoSlots):
		return "no_slots"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

// CheckYooKassa tries the shop's keys (GET /me) before the admin saves them.
func (s *Service) CheckYooKassa(ctx context.Context, shopID, secret string) error {
	y := yooKassa{base: s.d.YooKassaAPI, shopID: shopID, secret: secret, hc: s.d.HTTP}
	var me struct {
		AccountID string `json:"account_id"`
	}
	return y.do(ctx, http.MethodGet, "/me", "", nil, &me)
}

// CheckCryptoBot tries an app token (getMe) before the admin saves it.
func (s *Service) CheckCryptoBot(ctx context.Context, token string, testnet bool) error {
	base := s.d.CryptoBotAPI
	if testnet {
		base = s.d.CryptoBotTestAPI
	}
	var me struct {
		AppID int64 `json:"app_id"`
	}
	return cryptoBot{base: base, token: token, hc: s.d.HTTP}.call(ctx, "getMe", struct{}{}, &me)
}

// ErrorCode is a provider error's short code for the admin panel.
func ErrorCode(err error) string { return errCode(err) }
