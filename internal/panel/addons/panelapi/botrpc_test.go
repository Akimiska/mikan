package panelapi

import (
	"context"
	"reflect"
	"testing"

	"mikan/internal/panel/tgbot"
)

// The bot's RPC is the methods of tgbot.Backend and tgbot.Shop, and the rule table is what
// guards them: a method added to either without a rule is refused to the addon (and would
// be found only in production), a rule whose argument positions do not point at the
// Telegram accounts or subscriptions guards nothing, and a rule for a method that is gone
// is a promise nothing keeps.
func TestBotRPCRulesFitTheMethods(t *testing.T) {
	ctxType := reflect.TypeFor[context.Context]()
	int64Type := reflect.TypeFor[int64]()
	known := map[string]bool{}
	check := func(name string, m reflect.Type) {
		t.Helper()
		known[name] = true
		rl, ok := botRPC[name]
		if !ok {
			t.Errorf("%s has no rule in botRPC", name)
			return
		}
		var args []reflect.Type
		for i := range m.NumIn() {
			if m.In(i) != ctxType {
				args = append(args, m.In(i))
			}
		}
		for what, idx := range map[string][]int{"tg": rl.tg, "owner": rl.owner, "named": rl.named} {
			for _, i := range idx {
				if i < 0 || i >= len(args) || args[i] != int64Type {
					t.Errorf("%s: the %s argument %d is not an int64 of the method (%d arguments)", name, what, i, len(args))
				}
			}
		}
		if rl.tgField >= 0 {
			if rl.tgField >= len(args) || args[rl.tgField].Kind() != reflect.Struct {
				t.Errorf("%s: tgField %d is not a request object", name, rl.tgField)
			} else if f, ok := args[rl.tgField].FieldByName("TgID"); !ok || f.Type != int64Type || f.Tag.Get("json") != "" {
				t.Errorf("%s: the request object has no plain int64 TgID the rule reads", name)
			}
		}
	}
	backend := reflect.TypeFor[tgbot.Backend]()
	for i := range backend.NumMethod() {
		// Shop hands out the shop, whose methods are named "Shop.<method>".
		if m := backend.Method(i); m.Name != "Shop" {
			check(m.Name, m.Type)
		}
	}
	shop := reflect.TypeFor[tgbot.Shop]()
	for i := range shop.NumMethod() {
		check("Shop."+shop.Method(i).Name, shop.Method(i).Type)
	}
	for name := range botRPC {
		if !known[name] {
			t.Errorf("botRPC has a rule for %s, which is no method of Backend or Shop", name)
		}
	}
}

// Link takes only a subscription the panel named by a code or an address someone sent the
// bot: the answers of UserOfCode and UserOfAddress are what names one, an id the addon made
// up is not, and neither is an address that led nowhere.
func TestBotRPCLinkTakesANamedSubscription(t *testing.T) {
	var tk talking
	tk.noteNamed("UserOfCode", []byte(`{"results":[7]}`))
	tk.noteNamed("UserOfAddress", []byte(`{"results":[8,true]}`))
	tk.noteNamed("UserOfAddress", []byte(`{"results":[9,false]}`))
	tk.noteNamed("UserOfCode", []byte(`{"error":{"code":"link_invalid"}}`))
	tk.noteNamed("User", []byte(`{"results":[10]}`))
	for id, want := range map[int64]bool{7: true, 8: true, 9: false, 10: false, 11: false} {
		if got := tk.wasNamed(id); got != want {
			t.Errorf("subscription %d named: %v, want %v", id, got, want)
		}
	}
	if rl := botRPC["Link"]; len(rl.named) != 1 || rl.named[0] != 1 {
		t.Fatalf("Link's rule names no subscription: %+v", rl)
	}
	if rl := botRPC["Owner"]; len(rl.owner) != 1 || rl.owner[0] != 0 {
		t.Fatalf("Owner's rule does not ask for the owner: %+v", rl)
	}
}
