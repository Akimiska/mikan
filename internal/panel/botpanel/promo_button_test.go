package botpanel

import (
	"encoding/json"
	"strings"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgate"
	"mikan/internal/panel/tgbot"
	"mikan/internal/tgapi"
)

// menuTexts is the main menu as rows of button labels, and fails on an empty row.
func menuTexts(t *testing.T, kb *tgapi.Keyboard) [][]string {
	t.Helper()
	var out [][]string
	for _, r := range kb.InlineKeyboard {
		if len(r) == 0 {
			t.Fatalf("an empty row in the menu: %+v", kb.InlineKeyboard)
		}
		row := []string{}
		for _, b := range r {
			row = append(row, b.Text)
		}
		out = append(out, row)
	}
	return out
}

func hasPromo(rows [][]string) bool {
	for _, r := range rows {
		for _, b := range r {
			if strings.Contains(b, "Промокоды") {
				return true
			}
		}
	}
	return false
}

// menuOf is the main menu the account chat gets with cfg saved as the bot's setup: the
// keyboard the screens drew, as Telegram got it.
func (e *env) menuOf(cfg tgbot.Config, chat int64) *tgapi.Keyboard {
	e.t.Helper()
	if err := settings.Set(e.ctx, e.set, tgate.KeyConfig, cfg); err != nil {
		e.t.Fatal(err)
	}
	raw, err := json.Marshal(e.tap(chat, "m").body["reply_markup"])
	if err != nil {
		e.t.Fatal(err)
	}
	var kb tgapi.Keyboard
	if err := json.Unmarshal(raw, &kb); err != nil {
		e.t.Fatal(err)
	}
	return &kb
}

// linkAnother links one more subscription to the account.
func (e *env) linkAnother(tg int64) db.User {
	e.t.Helper()
	u, err := e.users.Create(e.ctx, domain.CreateInput{Name: "Вторая", TariffID: e.user.TariffID.Int64})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.Q.LinkTg(e.ctx, db.LinkTgParams{UserID: u.ID, TgID: tg, CreatedAt: 1}); err != nil {
		e.t.Fatal(err)
	}
	return u
}

// The «Промокоды» button is the admin's to hide. Hiding it takes only the menu entry:
// the Mini App page still has its promo field.
func TestPromoButtonInMenu(t *testing.T) {
	e := setup(t)
	e.link(555) // the menu of an account with one subscription

	cfg := tgbot.Default("ru")
	rows := menuTexts(t, e.menuOf(cfg, 555))
	if !hasPromo(rows) {
		t.Fatalf("a default bot shows the promo codes button: %v", rows)
	}
	if last := rows[len(rows)-1]; len(last) != 1 || !strings.Contains(last[0], "Промокоды") {
		t.Fatalf("it sits alone in the last row: %v", rows)
	}

	cfg.PromoButton = false
	kb := e.menuOf(cfg, 555)
	rows = menuTexts(t, kb)
	if hasPromo(rows) {
		t.Fatalf("hidden, the button is gone: %v", rows)
	}
	for _, r := range kb.InlineKeyboard {
		for _, b := range r {
			if b.WebApp != nil && strings.HasSuffix(b.WebApp.URL, "#promocodes") {
				t.Fatalf("no way in to the promo section is left: %+v", b)
			}
		}
	}
	// The other buttons stay as they were: sub + devices, connect, renew + support, the page.
	if len(rows) != 4 || len(rows[0]) != 2 || len(rows[1]) != 1 || len(rows[2]) != 2 || len(rows[3]) != 1 {
		t.Fatalf("the rest of the menu is unchanged: %v", rows)
	}
	var page *tgapi.Button
	for _, r := range kb.InlineKeyboard {
		for i := range r {
			if r[i].WebApp != nil {
				page = &r[i]
			}
		}
	}
	if page == nil || page.WebApp.URL != "https://vpn.example.com:21355/sub/tg" {
		t.Fatalf("the Mini App page button stays: %+v", page)
	}

	// Subscriptions switcher still comes last and the menu has no empty row.
	second := e.linkAnother(555)
	rows = menuTexts(t, e.menuOf(cfg, 555))
	if last := rows[len(rows)-1]; len(last) != 1 || !strings.HasPrefix(last[0], "🔁 Подписки") {
		t.Fatalf("the subscriptions button: %v", rows)
	}
	if err := e.st.Q.UnlinkTg(e.ctx, second.ID); err != nil {
		t.Fatal(err)
	}

	// Without the Mini App there is no promo button either way.
	cfg.PromoButton = true
	cfg.MiniApp = false
	if rows = menuTexts(t, e.menuOf(cfg, 555)); hasPromo(rows) {
		t.Fatalf("no Mini App, no promo button: %v", rows)
	}
}

// Hiding other buttons leaves no empty row and no row of the promo button's own.
func TestMenuRowsAfterHiding(t *testing.T) {
	e := setup(t)
	e.link(555)
	cfg := tgbot.Default("ru")
	for i := range cfg.Buttons {
		if cfg.Buttons[i].Action == "sub" {
			cfg.Buttons[i].On = false // devices keeps its «in one row» flag with nothing before it
		}
	}
	cfg.PromoButton = false
	rows := menuTexts(t, e.menuOf(cfg, 555))
	if len(rows[0]) != 1 || !strings.Contains(rows[0][0], "Устройства") {
		t.Fatalf("the first button starts a row of its own: %v", rows)
	}
}

// A setup saved by the panel with the option off keeps it off; one saved before the option
// existed shows the button.
func TestPromoButtonSaved(t *testing.T) {
	e := setup(t)
	cfg := tgbot.Default("ru")
	cfg.PromoButton = false
	if err := settings.Set(e.ctx, e.set, tgate.KeyConfig, cfg); err != nil {
		t.Fatal(err)
	}
	if e.backend.Config(e.ctx).PromoButton {
		t.Fatal("a saved off stays off")
	}
	cfg.PromoButton = true
	if err := settings.Set(e.ctx, e.set, tgate.KeyConfig, cfg); err != nil {
		t.Fatal(err)
	}
	if !e.backend.Config(e.ctx).PromoButton {
		t.Fatal("a saved on stays on")
	}
}
