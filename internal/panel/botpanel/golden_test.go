package botpanel

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/model"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgbot"
)

// goldenDir holds what crossed the bot's RPC before the bot's side and the panel's were
// split (internal/panel/model/testdata): the JSON of the panel's own values then.
const goldenDir = "../model/testdata"

// goldenFile is the golden of name.
func goldenFile(t *testing.T, name string) []byte {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(goldenDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may turn the files' line ends into CRLF.
	return bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
}

// golden compares the JSON of v with the golden of name.
func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if got, want := append(got, '\n'), goldenFile(t, name); !bytes.Equal(got, want) {
		t.Errorf("%s crosses differently:\n got %s\nwant %s", name, got, want)
	}
}

// readGolden reads the golden of name into v, as the panel reads a call's argument.
func readGolden(t *testing.T, name string, v any) {
	t.Helper()
	if err := json.Unmarshal(goldenFile(t, name), v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// The panel's values, converted to the model as Local and shop convert them, cross the RPC
// byte for byte as the panel's values themselves did: an installed bot reads what it read
// before. The samples are those the goldens were written from.
func TestWireGoldens(t *testing.T) {
	nullInt := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	nullString := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	u := db.User{ID: 101, Name: "Анна", Contact: "@anna", Note: "note <b>", Tags: "tg,vip", Status: "active", TariffID: nullInt(2),
		TrafficLimit: nullInt(161061273600), DeviceLimit: nullInt(3), ResetStrategy: "period", PeriodDays: 30, PeriodStart: 1800000000,
		UsedUp: 1024, UsedDown: 2048, TotalUp: 4096, TotalDown: 8192, ExpiresAt: nullInt(1802592000), Inbounds: nullString("[1,2]"),
		SubToken: "AbCdEfGhIjKlMnOpQrStUvWx", SlotID: nullInt(7), OnlineAt: nullInt(1800000100), CreatedAt: 1799990000, UpdatedAt: 1800000200,
		BillingDay: nullInt(5), Source: "bot", Hidden: 1, FolderID: nullInt(3), SpeedLimit: nullInt(50), DeviceMode: "bound", ExtraDevices: 1,
		ExtraDevicesUntil: nullInt(1802592000)}
	chat := db.TgChat{TgID: 555, Username: "anna", FirstName: "Анна", MenuMsgID: 1000, Current: 101, Blocked: 1, CreatedAt: 1799990000,
		UpdatedAt: 1800000200, CampaignID: nullInt(4), CampaignAt: nullInt(1799990001)}
	device := db.BoundDevice{ID: 11, UserID: 101, Hwid: "hw-1", SlotID: 7, Os: "Android", OsVersion: "15", Model: "Pixel 9", App: "Happ",
		LastIp: "203.0.113.5", CreatedAt: 1799990000, LastSeen: 1800000100, Name: "Телефон"}
	tariff := db.Tariff{ID: 2, Name: "Стандарт", TrafficLimit: nullInt(161061273600), DurationDays: 30, DeviceLimit: nullInt(3), ResetStrategy: "period",
		PriceLabel: "199 ₽", Sort: 1, Archived: 1, CreatedAt: 1799000000, BillingDay: nullInt(5), PriceStars: nullInt(150), PriceRub: nullInt(19900), OnSale: 1,
		SpeedLimit: nullInt(50), Description: "С белыми списками", DeviceMode: "online", DevicePriceStars: nullInt(40), DevicePriceRub: nullInt(4900),
		DeviceExtraMax: 2, ChosenServers: 1}
	pay := db.Payment{ID: 31, Provider: "stars", Payload: "pl-31", ExternalID: nullString("ch-31"), TgID: 555, Kind: "renew", UserID: nullInt(101),
		TariffID: nullInt(2), PackageID: nullInt(9), TariffName: "Стандарт", Amount: 150, Currency: "XTR", Status: "applied", Error: "none",
		PayUrl: "https://t.me/$inv", CreatedAt: 1800000000, PaidAt: nullInt(1800000010), AppliedAt: nullInt(1800000011), RefundedAt: nullInt(1800000012),
		TermDays: nullInt(90), Revert: `{"days":90}`}
	top := db.Topup{ID: 41, TgID: 555, Amount: 50000, Currency: "RUB", Photo: "file-1", Status: "pending", CreatedAt: 1800000000,
		DecidedAt: nullInt(1800000100), AdminID: nullInt(1), Reason: "blurry"}
	op := db.BalanceOp{ID: 51, TgID: 555, Amount: -19900, Kind: "spend", PaymentID: nullInt(31), TopupID: nullInt(41), AdminID: nullInt(1), Note: "renew",
		CreatedAt: 1800000000}
	pack := db.TrafficPackage{ID: 9, Name: "+50 ГБ", Bytes: 53687091200, PoolID: nullInt(6), Lifetime: "period", Days: 30, PriceStars: nullInt(30),
		PriceRub: nullInt(9900), OnSale: 1, Sort: 2, Archived: 1, CreatedAt: 1799000000}
	balance := billing.BalanceConfig{Enabled: true, Currency: "USD", Rate: 95.5, Details: "карта 1234", Min: 100, Max: 100000}
	config := billing.Config{Enabled: true, Stars: true, AllowNew: true, RenewResetsTraffic: true, TrialTariffID: 4, TrialStars: 10,
		TrialRub: 9900, FreeTariffID: 5, FreeCondition: "channel", ExtraDevicesKeep: true, Balance: balance}
	at := time.Unix(1800003600, 0).UTC()
	users := func(list []db.User) []model.User {
		return model.Each(list, func(u db.User) model.User { return model.User(u) })
	}

	for name, v := range map[string]any{
		// Local's rows.
		"User":        model.User(u),
		"User.zero":   model.User(db.User{}),
		"TgChat":      model.TgChat(chat),
		"BoundDevice": model.BoundDevice(device),
		"Tariff":      model.Tariff(tariff),
		"Tariff.zero": model.Tariff(db.Tariff{}),
		// The shop's rows.
		"Payment":        payment(pay),
		"Payment.zero":   payment(db.Payment{}),
		"Topup":          topup(top),
		"BalanceOp":      model.BalanceOp(op),
		"TrafficPackage": model.TrafficPackage(pack),
		// The shop as billing has it.
		"BalanceConfig":      model.BalanceConfig(balance),
		"BalanceConfig.zero": model.BalanceConfig(billing.BalanceConfig{}),
		"Config":             config.Model(),
		"Config.zero":        billing.Config{}.Model(),
		"Available":          billing.Available{Stars: true, Addons: []string{"yookassa", "cryptobot"}, Balance: balance}.Model(),
		"Available.zero":     billing.Available{}.Model(),
		"Offer": billing.Offer{Tariff: tariff, Terms: []billing.OfferTerm{{Days: 30, Stars: 150, Rub: 19900}, {Days: 90, Stars: 400, Rub: 49900}},
			First: 30, Stars: 150, Rub: 19900, Groups: []billing.OfferGroup{{Name: "WL", Mode: "own", Limit: 53687091200}, {Name: "Premium", Mode: "closed"}},
			Gifts: []billing.OfferGift{{Package: pack, Pool: "WL"}}}.Model(),
		"Offer.zero":   billing.Offer{}.Model(),
		"PackageOffer": billing.PackageOffer{Package: pack, Pool: "WL", Stars: 30, Rub: 9900}.Model(),
		"DeviceOffer":  billing.DeviceOffer{Limit: 3, Extra: 1, Max: 2, Stars: 40, Rub: 4900, Keep: true, Expiry: nullInt(1802592000)}.Model(),
		"TrialOffer":   billing.TrialOffer{Tariff: tariff, Stars: 10, Rub: 9900}.Model(),
		"FreeOffer":    billing.FreeOffer{Tariff: tariff, Channel: "@mikan"}.Model(),
		// The rules of the subscriber's own unbinding and new link.
		"UnbindState": domain.UnbindState{Rules: domain.UnbindRules{Off: true, Limit: 2, Days: 30, ReturnHours: domain.ReturnNever}, Used: 1,
			Next: at}.Model(),
		"UnbindState.zero":  domain.UnbindState{}.Model(),
		"ReissueState":      domain.ReissueState{Rules: domain.ReissueRules{Off: true, Limit: 3, Days: 7}, Used: 2, Next: at}.Model(),
		"ReissueState.zero": domain.ReissueState{}.Model(),
		// An account and a link as Local makes them of the rows (Context, Link).
		"Account":      tgbot.Account{Chat: model.TgChat(chat), HasChat: true, Links: users([]db.User{u})},
		"Account.zero": tgbot.Account{Links: users(nil)},
		"Linked":       tgbot.Linked{Status: tgbot.LinkTaken, User: model.User(u), Owner: 556},
	} {
		t.Run(name, func(t *testing.T) { golden(t, name, v) })
	}
}

// The requests go the other way. A bot sends the model's request, which is billing's as the
// bot sent it before; the panel reads it as the model's and the shop converts it to
// billing's (shop.Invoice, PackageInvoice and DeviceInvoice), which is the request billing
// read before.
func TestWireGoldenRequests(t *testing.T) {
	days := int64(90)
	invoice := billing.InvoiceRequest{TgID: 555, UserID: 101, TariffID: 2, TermDays: &days, Provider: "stars", PromoCode: "SPRING"}
	pack := billing.PackageRequest{TgID: 555, UserID: 101, PackageID: 9, Provider: "addon:yookassa", PromoCode: "SPRING"}
	device := billing.DeviceRequest{TgID: 555, UserID: 101, Provider: "balance"}
	golden(t, "InvoiceRequest", model.InvoiceRequest(invoice))
	golden(t, "InvoiceRequest.zero", model.InvoiceRequest(billing.InvoiceRequest{}))
	golden(t, "PackageRequest", model.PackageRequest(pack))
	golden(t, "DeviceRequest", model.DeviceRequest(device))

	var (
		inv, none model.InvoiceRequest
		pk        model.PackageRequest
		dev       model.DeviceRequest
	)
	readGolden(t, "InvoiceRequest", &inv)
	readGolden(t, "InvoiceRequest.zero", &none)
	readGolden(t, "PackageRequest", &pk)
	readGolden(t, "DeviceRequest", &dev)
	for name, c := range map[string]struct{ got, want any }{
		"InvoiceRequest":      {billing.InvoiceRequest(inv), invoice},
		"InvoiceRequest.zero": {billing.InvoiceRequest(none), billing.InvoiceRequest{}},
		"PackageRequest":      {billing.PackageRequest(pk), pack},
		"DeviceRequest":       {billing.DeviceRequest(dev), device},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s reads as %+v, want %+v", name, c.got, c.want)
		}
	}
}
