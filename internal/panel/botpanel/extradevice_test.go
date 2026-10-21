package botpanel

import (
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"testing"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/model"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/tgapi"
)

// "Докупить устройство" in the subscription screen: the offer says what it gives, the
// invoice is for the device, and once paid the buyer is told how many devices there are.
func TestBuyExtraDevice(t *testing.T) {
	var svc *billing.Service
	e := setup(t, func(e *env, d *wiring) {
		pool := domain.NewPool(e.st, e.clock)
		svc = billing.New(billing.Deps{Store: e.st, Settings: e.set, Users: domain.NewUsers(e.st, pool, noChanges{}, e.clock),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: e.clock, MaxLinks: model.MaxLinks})
		d.Billing = svc
	})
	svc.SetTelegram(e.gate)
	const owner = 555
	q := e.st.Q
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(settings.Set(e.ctx, e.set, billing.KeyConfig, billing.Config{Enabled: true, Stars: true, AllowNew: true}))
	must(q.LinkTg(e.ctx, db.LinkTgParams{UserID: e.user.ID, TgID: owner, CreatedAt: 1}))
	menu := int64(1000)
	step := func(data string) call {
		t.Helper()
		e.later()
		n := e.tg.count()
		e.press(owner, menu, data)
		edit, _ := find(e.tg.wait(t, n, "editMessageText"), "editMessageText")
		return edit
	}
	if _, ok := buttons(step("s"))["📱 Докупить устройство"]; ok {
		t.Fatal("no device for sale yet")
	}
	must(q.SetTariffExtraDevices(e.ctx, db.SetTariffExtraDevicesParams{DevicePriceStars: sql.NullInt64{Int64: 40, Valid: true}, DeviceExtraMax: 1, ID: e.user.TariffID.Int64}))
	sub := step("s")
	if buttons(sub)["📱 Докупить устройство"] != "e" {
		t.Fatalf("subscription screen: %q %v", text(sub), buttons(sub))
	}
	offer := step("e")
	if !strings.Contains(text(offer), "Сейчас устройств: 3, станет 4") || buttons(offer)["⭐ Telegram Stars — ⭐ 40"] != "ep:s" {
		t.Fatalf("offer: %q %v", text(offer), buttons(offer))
	}
	n := e.tg.count()
	inv := step("ep:s")
	link, ok := find(e.tg.until(t, n, func(cs []call) bool { _, ok := find(cs, "createInvoiceLink"); return ok }), "createInvoiceLink")
	if !ok || !strings.HasPrefix(buttons(inv)["Оплатить ⭐ 40"], "https://t.me/$") {
		t.Fatalf("invoice: %q %v", text(inv), buttons(inv))
	}
	payload, _ := link.body["payload"].(string)
	n = e.tg.count()
	e.tg.push(tgapi.Update{Message: &tgapi.Message{MessageID: 99, From: &tgapi.User{ID: owner}, Chat: tgapi.Chat{ID: owner, Type: "private"},
		SuccessfulPayment: &tgapi.SuccessfulPayment{Currency: "XTR", TotalAmount: 40, InvoicePayload: payload, ChargeID: "tg-dev-1"}}})
	e.tg.until(t, n, func(cs []call) bool {
		for _, c := range cs {
			if c.method == "sendMessage" && strings.Contains(text(c), "к подписке «Анна» добавлено устройство, теперь их 4") {
				return true
			}
		}
		return false
	})
	// The tariff's cap is reached: the button goes.
	if _, ok := buttons(step("s"))["📱 Докупить устройство"]; ok {
		t.Fatal("the cap is reached, the button stays")
	}
}
