package model

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The goldens (testdata) are what the bot's RPC carried before the bot's side and the
// panel's were split: each is json.Marshal of a sample of the panel's own types then (db,
// billing, domain, tgbot) and a line end. The model's types carry the same byte for byte, so
// an installed bot reads what it read before; botpanel holds the panel's conversions to the
// model to the same files, tgbot its own types (Account, Linked, Pool, Config) and the
// methods of the RPC (methods.txt).

// golden compares the JSON of v with testdata/<name>.json.
func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may turn the files' line ends into CRLF.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if got = append(got, '\n'); !bytes.Equal(got, want) {
		t.Errorf("%s crosses differently:\n got %s\nwant %s", name, got, want)
	}
}

// The model's values, with the fields of the samples the goldens were written from, cross
// as those did.
func TestWireGoldens(t *testing.T) {
	nullInt := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	nullString := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	user := User{ID: 101, Name: "Анна", Contact: "@anna", Note: "note <b>", Tags: "tg,vip", Status: "active", TariffID: nullInt(2),
		TrafficLimit: nullInt(161061273600), DeviceLimit: nullInt(3), ResetStrategy: "period", PeriodDays: 30, PeriodStart: 1800000000,
		UsedUp: 1024, UsedDown: 2048, TotalUp: 4096, TotalDown: 8192, ExpiresAt: nullInt(1802592000), Inbounds: nullString("[1,2]"),
		SubToken: "AbCdEfGhIjKlMnOpQrStUvWx", SlotID: nullInt(7), OnlineAt: nullInt(1800000100), CreatedAt: 1799990000, UpdatedAt: 1800000200,
		BillingDay: nullInt(5), Source: "bot", Hidden: 1, FolderID: nullInt(3), SpeedLimit: nullInt(50), DeviceMode: "bound", ExtraDevices: 1,
		ExtraDevicesUntil: nullInt(1802592000)}
	chat := TgChat{TgID: 555, Username: "anna", FirstName: "Анна", MenuMsgID: 1000, Current: 101, Blocked: 1, CreatedAt: 1799990000,
		UpdatedAt: 1800000200, CampaignID: nullInt(4), CampaignAt: nullInt(1799990001)}
	device := BoundDevice{ID: 11, UserID: 101, Hwid: "hw-1", SlotID: 7, Os: "Android", OsVersion: "15", Model: "Pixel 9", App: "Happ",
		LastIp: "203.0.113.5", CreatedAt: 1799990000, LastSeen: 1800000100, Name: "Телефон"}
	tariff := Tariff{ID: 2, Name: "Стандарт", TrafficLimit: nullInt(161061273600), DurationDays: 30, DeviceLimit: nullInt(3), ResetStrategy: "period",
		PriceLabel: "199 ₽", Sort: 1, Archived: 1, CreatedAt: 1799000000, BillingDay: nullInt(5), PriceStars: nullInt(150), PriceRub: nullInt(19900), OnSale: 1,
		SpeedLimit: nullInt(50), Description: "С белыми списками", DeviceMode: "online", DevicePriceStars: nullInt(40), DevicePriceRub: nullInt(4900),
		DeviceExtraMax: 2, ChosenServers: 1}
	payment := Payment{ID: 31, Provider: "stars", Payload: "pl-31", ExternalID: nullString("ch-31"), TgID: 555, Kind: "renew", UserID: nullInt(101),
		TariffID: nullInt(2), PackageID: nullInt(9), TariffName: "Стандарт", Amount: 150, Currency: "XTR", Status: "applied", Error: "none",
		PayUrl: "https://t.me/$inv", CreatedAt: 1800000000, PaidAt: nullInt(1800000010), AppliedAt: nullInt(1800000011), RefundedAt: nullInt(1800000012),
		TermDays: nullInt(90), Revert: `{"days":90}`}
	topup := Topup{ID: 41, TgID: 555, Amount: 50000, Currency: "RUB", Photo: "file-1", Status: "pending", CreatedAt: 1800000000,
		DecidedAt: nullInt(1800000100), AdminID: nullInt(1), Reason: "blurry"}
	op := BalanceOp{ID: 51, TgID: 555, Amount: -19900, Kind: "spend", PaymentID: nullInt(31), TopupID: nullInt(41), AdminID: nullInt(1), Note: "renew",
		CreatedAt: 1800000000}
	pack := TrafficPackage{ID: 9, Name: "+50 ГБ", Bytes: 53687091200, PoolID: nullInt(6), Lifetime: "period", Days: 30, PriceStars: nullInt(30),
		PriceRub: nullInt(9900), OnSale: 1, Sort: 2, Archived: 1, CreatedAt: 1799000000}
	balance := BalanceConfig{Enabled: true, Currency: "USD", Rate: 95.5, Details: "карта 1234", Min: 100, Max: 100000}
	config := Config{Enabled: true, Stars: true, AllowNew: true, RenewResetsTraffic: true, TrialTariffID: 4, TrialStars: 10,
		TrialRub: 9900, FreeTariffID: 5, FreeCondition: "channel", ExtraDevicesKeep: true, Balance: balance}
	days := int64(90)
	at := time.Unix(1800003600, 0).UTC()

	for name, v := range map[string]any{
		"User": user, "TgChat": chat, "BoundDevice": device, "Tariff": tariff, "Payment": payment, "Topup": topup,
		"BalanceOp": op, "TrafficPackage": pack,
		"User.zero": User{}, "Tariff.zero": Tariff{}, "Payment.zero": Payment{},
		"BalanceConfig": balance, "BalanceConfig.zero": BalanceConfig{},
		"Config": config, "Config.zero": Config{},
		"Available":      Available{Stars: true, Addons: []string{"yookassa", "cryptobot"}, Balance: balance},
		"Available.zero": Available{},
		"Offer": Offer{Tariff: tariff, Terms: []OfferTerm{{Days: 30, Stars: 150, Rub: 19900}, {Days: 90, Stars: 400, Rub: 49900}},
			First: 30, Stars: 150, Rub: 19900, Groups: []OfferGroup{{Name: "WL", Mode: "own", Limit: 53687091200}, {Name: "Premium", Mode: "closed"}},
			Gifts: []OfferGift{{Package: pack, Pool: "WL"}}},
		"Offer.zero":          Offer{},
		"PackageOffer":        PackageOffer{Package: pack, Pool: "WL", Stars: 30, Rub: 9900},
		"DeviceOffer":         DeviceOffer{Limit: 3, Extra: 1, Max: 2, Stars: 40, Rub: 4900, Keep: true, Expiry: nullInt(1802592000)},
		"TrialOffer":          TrialOffer{Tariff: tariff, Stars: 10, Rub: 9900},
		"FreeOffer":           FreeOffer{Tariff: tariff, Channel: "@mikan"},
		"InvoiceRequest":      InvoiceRequest{TgID: 555, UserID: 101, TariffID: 2, TermDays: &days, Provider: "stars", PromoCode: "SPRING"},
		"InvoiceRequest.zero": InvoiceRequest{},
		"PackageRequest":      PackageRequest{TgID: 555, UserID: 101, PackageID: 9, Provider: "addon:yookassa", PromoCode: "SPRING"},
		"DeviceRequest":       DeviceRequest{TgID: 555, UserID: 101, Provider: "balance"},
		"UnbindState":         UnbindState{Rules: UnbindRules{Off: true, Limit: 2, Days: 30, ReturnHours: ReturnNever}, Used: 1, Next: at},
		"UnbindState.zero":    UnbindState{},
		"ReissueState":        ReissueState{Rules: ReissueRules{Off: true, Limit: 3, Days: 7}, Used: 2, Next: at},
		"ReissueState.zero":   ReissueState{},
	} {
		t.Run(name, func(t *testing.T) { golden(t, name, v) })
	}
}
