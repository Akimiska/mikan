package tgbot

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"mikan/internal/panel/model"
)

// goldenDir holds what crossed the bot's RPC before the bot's side and the panel's were
// split (internal/panel/model/testdata), and the methods of the RPC then (methods.txt).
const goldenDir = "../model/testdata"

// goldenFile is the file name of goldenDir.
func goldenFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDir, name))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may turn the files' line ends into CRLF.
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// The bot's own types cross the RPC byte for byte as before the split: an installed bot and
// this one read each other's. Account and Linked hold the model's rows now.
func TestWireGoldens(t *testing.T) {
	nullInt := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	user := model.User{ID: 101, Name: "Анна", Contact: "@anna", Note: "note <b>", Tags: "tg,vip", Status: "active", TariffID: nullInt(2),
		TrafficLimit: nullInt(161061273600), DeviceLimit: nullInt(3), ResetStrategy: "period", PeriodDays: 30, PeriodStart: 1800000000,
		UsedUp: 1024, UsedDown: 2048, TotalUp: 4096, TotalDown: 8192, ExpiresAt: nullInt(1802592000), Inbounds: sql.NullString{String: "[1,2]", Valid: true},
		SubToken: "AbCdEfGhIjKlMnOpQrStUvWx", SlotID: nullInt(7), OnlineAt: nullInt(1800000100), CreatedAt: 1799990000, UpdatedAt: 1800000200,
		BillingDay: nullInt(5), Source: "bot", Hidden: 1, FolderID: nullInt(3), SpeedLimit: nullInt(50), DeviceMode: "bound", ExtraDevices: 1,
		ExtraDevicesUntil: nullInt(1802592000)}
	chat := model.TgChat{TgID: 555, Username: "anna", FirstName: "Анна", MenuMsgID: 1000, Current: 101, Blocked: 1, CreatedAt: 1799990000,
		UpdatedAt: 1800000200, CampaignID: nullInt(4), CampaignAt: nullInt(1799990001)}
	for name, v := range map[string]any{
		"Account":        Account{Chat: chat, HasChat: true, Links: []model.User{user}},
		"Account.zero":   Account{},
		"Linked":         Linked{Status: LinkTaken, User: user, Owner: 556},
		"Pool":           Pool{Name: "WL", Used: 1024, Limit: 53687091200, Extra: 2048, Exhausted: true},
		"BotConfig.ru":   Default("ru"),
		"BotConfig.en":   Default("en"),
		"BotConfig.zero": Config{},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := append(got, '\n'), goldenFile(t, name+".json"); !bytes.Equal(got, want) {
				t.Errorf("%s crosses differently:\n got %s\nwant %s", name, got, want)
			}
		})
	}
}

// qualifier is a package's name before a type: db.User and model.User are both User.
var qualifier = regexp.MustCompile(`\b[a-z][a-z0-9_]*\.`)

// signature is a method's arguments and results as methods.txt has them, "(int64, string)
// (bool)": the context and the error left out.
func signature(f reflect.Type) string {
	var in, out []string
	for i := range f.NumIn() {
		if f.In(i) != contextType {
			in = append(in, f.In(i).String())
		}
	}
	for i := range f.NumOut() {
		if f.Out(i) != errorType {
			out = append(out, f.Out(i).String())
		}
	}
	return "(" + strings.Join(in, ", ") + ") (" + strings.Join(out, ", ") + ")"
}

// An installed bot calls the panel by the methods of Backend and Shop: methods.txt has them
// as they were before the split, with the types of the packages then. Every one of them is
// still there with the same arguments and results, the packages left out, and nothing is
// there besides them: a method added goes into the file with it. GrantsLeft keeps its name
// and place; only its JSON changed, on purpose (model.GrantsLeft).
func TestRPCMethodsAsBefore(t *testing.T) {
	was := map[string]string{} // a method's name: its line, the packages left out
	for _, line := range strings.Split(strings.TrimSpace(string(goldenFile(t, "methods.txt"))), "\n") {
		name, _, _ := strings.Cut(line, "(")
		was[name] = qualifier.ReplaceAllString(line, "")
	}
	now := map[string]string{} // the same of the interfaces now
	for prefix, it := range map[string]reflect.Type{"": backendType, "Shop.": shopType} {
		for i := range it.NumMethod() {
			m := it.Method(i)
			if prefix == "" && m.Name == "Shop" {
				continue // the way to Shop's methods, not a call of its own
			}
			now[prefix+m.Name] = prefix + m.Name + signature(m.Type)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(was)) {
		switch got, ok := now[name]; {
		case !ok:
			t.Errorf("%s is gone: an installed bot calls it", name)
		case qualifier.ReplaceAllString(got, "") != was[name]:
			t.Errorf("%s changed: was %q, now %q", name, was[name], got)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(now)) {
		if _, ok := was[name]; !ok {
			t.Errorf("%s is new: add %q to methods.txt", name, now[name])
		}
	}
}
