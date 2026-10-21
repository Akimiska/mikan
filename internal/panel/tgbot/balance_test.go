package tgbot

import (
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"testing"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgate"
)

// "💰 Баланс" in the menu: a top-up takes its amount as a message and its screenshot as a
// picture, the admin's approval is told, and the balance pays a renewal at once.
func TestBalanceTopupAndPay(t *testing.T) {
	var svc *billing.Service
	e := setup(t, func(e *env, d *wiring) {
		pool := domain.NewPool(e.st, e.clock)
		svc = billing.New(billing.Deps{Store: e.st, Settings: e.set, Users: domain.NewUsers(e.st, pool, noChanges{}, e.clock),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: e.clock, MaxLinks: tgate.MaxLinks})
		d.Billing = svc
	})
	svc.SetTelegram(e.gate)
	svc.SetNotifier(e.notifications())
	const owner = 555
	q := e.st.Q
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(settings.Set(e.ctx, e.set, billing.KeyConfig, billing.Config{Enabled: true, Stars: true, AllowNew: true,
		Balance: billing.BalanceConfig{Enabled: true, Currency: "RUB", Rate: 1, Details: "Карта 2200 0000 0000 0000"}}))
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
	main := step("m")
	if buttons(main)["💰 Баланс: 0 ₽"] != "bl" {
		t.Fatalf("menu: %v", buttons(main))
	}
	if b := buttons(step("bl")); b["➕ Пополнить"] != "bt" {
		t.Fatalf("balance: %v", b)
	}
	ask := step("bt")
	if !strings.Contains(text(ask), "напишите её сообщением") || buttons(ask)["500 ₽"] != "ba:50000" {
		t.Fatalf("amount: %q %v", text(ask), buttons(ask))
	}
	// The amount as a message: the screen asks for the transfer and its screenshot.
	n := e.tg.count()
	e.tg.push(tgate.Update{Message: &tgate.Message{MessageID: 50, From: &tgate.User{ID: owner}, Chat: tgate.Chat{ID: owner, Type: "private"}, Text: "750"}})
	e.tg.until(t, n, func(cs []call) bool {
		for _, c := range cs {
			if c.method == "sendMessage" && strings.Contains(text(c), "Переведите 750 ₽") && strings.Contains(text(c), "Карта 2200") {
				return true
			}
		}
		return false
	})
	n = e.tg.count()
	e.tg.push(tgate.Update{Message: &tgate.Message{MessageID: 51, From: &tgate.User{ID: owner}, Chat: tgate.Chat{ID: owner, Type: "private"},
		Photo: []tgate.PhotoSize{{FileID: "small"}, {FileID: "big"}}}})
	e.tg.until(t, n, func(cs []call) bool {
		for _, c := range cs {
			if c.method == "sendMessage" && strings.Contains(text(c), "Заявка на 750 ₽ отправлена") {
				return true
			}
		}
		return false
	})
	tops, _ := q.ListTopups(e.ctx, db.ListTopupsParams{Status: billing.TopupPending, Lim: 10})
	if len(tops) != 1 || tops[0].Photo != "big" || tops[0].Amount != 75000 {
		t.Fatalf("the request: %+v", tops)
	}
	n = e.tg.count()
	if _, _, err := svc.DecideTopup(e.ctx, tops[0].ID, true, 1, ""); err != nil {
		t.Fatal(err)
	}
	feed{e}.drain()
	e.tg.until(t, n, func(cs []call) bool {
		for _, c := range cs {
			if c.method == "sendMessage" && strings.Contains(text(c), "Баланс пополнен на 750 ₽. Теперь на нём 750 ₽") {
				return true
			}
		}
		return false
	})
	// One more device from the balance: paid at once, no invoice.
	must(q.SetTariffExtraDevices(e.ctx, db.SetTariffExtraDevicesParams{DevicePriceRub: sql.NullInt64{Int64: 10000, Valid: true}, DeviceExtraMax: 1, ID: e.user.TariffID.Int64}))
	offer := step("e")
	if buttons(offer)["💰 С баланса · 100 ₽"] != "ep:b" {
		t.Fatalf("no balance button: %v", buttons(offer))
	}
	paid := step("ep:b")
	if !strings.Contains(text(paid), "Оплачено с баланса: 100 ₽") {
		t.Fatalf("paid: %q", text(paid))
	}
	if left, _ := svc.BalanceOf(e.ctx, owner); left != 65000 {
		t.Fatalf("left: %d", left)
	}
	if u, _ := q.GetUser(e.ctx, e.user.ID); u.ExtraDevices != 1 {
		t.Fatalf("the device: %+v", u)
	}
}
