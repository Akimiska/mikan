package tgate

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/storetest"
)

// fakeTelegram is the Bot API for tests: it hands out queued updates and records calls.
type fakeTelegram struct {
	mu      sync.Mutex
	updates []Update // not yet confirmed by an offset past them, as at Telegram
	seq     int64
	offsets []int64 // the offset of each getUpdates
	calls   []call
	nextID  int64
	blocked map[int64]bool // chats that blocked the bot
	broken  map[string]bool
	stall   map[string]time.Duration
	// conflict: another program polls the token, so getUpdates gets Telegram's 409
	conflict bool
	// The channel: who is in it (account → status; the bot is its admin unless notAdmin),
	// and notAdmin, the bot is not, so getChatMember is refused.
	members  map[int64]string
	notAdmin bool
}

// join puts an account in the channel ("" takes it out).
func (f *fakeTelegram) join(id int64, status string) {
	f.mu.Lock()
	if f.members == nil {
		f.members = map[int64]string{}
	}
	f.members[id] = status
	f.mu.Unlock()
}

// set breaks (answers with something that is no Bot API reply) or fixes a method.
func (f *fakeTelegram) set(method string, broken bool) {
	f.mu.Lock()
	if f.broken == nil {
		f.broken = map[string]bool{}
	}
	f.broken[method] = broken
	f.mu.Unlock()
}

type call struct {
	method string
	body   map[string]any
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	ok := func(result any) { _ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result}) }
	switch method {
	case "getMe":
		ok(User{ID: 1, IsBot: true, FirstName: "Mikan", Username: "mikan_test_bot"})
		return
	case "getUpdates":
		f.mu.Lock()
		conflict := f.conflict
		f.mu.Unlock()
		if conflict {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 409,
				"description": "Conflict: terminated by other getUpdates request; make sure that only one bot instance is running"})
			return
		}
		off, _ := body["offset"].(float64)
		f.mu.Lock()
		f.offsets = append(f.offsets, int64(off))
		f.mu.Unlock()
		deadline := time.Now().Add(200 * time.Millisecond)
		for time.Now().Before(deadline) {
			f.mu.Lock()
			keep := f.updates[:0]
			for _, u := range f.updates {
				if u.UpdateID >= int64(off) {
					keep = append(keep, u)
				}
			}
			f.updates = keep
			ups := append([]Update(nil), keep...)
			f.mu.Unlock()
			if len(ups) > 0 {
				ok(ups)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		ok([]Update{})
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, call{method, body})
	chat, _ := body["chat_id"].(float64)
	blocked := f.blocked[int64(chat)]
	f.nextID++
	id := f.nextID
	broken, stall := f.broken[method], f.stall[method]
	f.mu.Unlock()
	if stall > 0 {
		select {
		case <-time.After(stall):
		case <-r.Context().Done():
		}
	}
	if broken {
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
		return
	}
	if blocked {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 403, "description": "Forbidden: bot was blocked by the user"})
		return
	}
	switch method {
	case "createInvoiceLink":
		ok("https://t.me/$inv" + strconv.FormatInt(id, 10))
	case "sendMessage":
		ok(Message{MessageID: 1000 + id, Chat: Chat{ID: int64(chat), Type: "private"}})
	case "getChatMember":
		user, _ := body["user_id"].(float64)
		f.mu.Lock()
		status, notAdmin := f.members[int64(user)], f.notAdmin
		f.mu.Unlock()
		switch {
		case notAdmin:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: member list is inaccessible"})
			return
		case int64(user) == 1:
			status = "administrator"
		case status == "":
			status = "left"
		}
		ok(ChatMember{Status: status, IsMember: status == "restricted"})
	default:
		ok(true)
	}
}

func (f *fakeTelegram) push(u Update) {
	f.mu.Lock()
	f.seq++
	u.UpdateID = 1000 + f.seq
	f.updates = append(f.updates, u)
	f.mu.Unlock()
}

// wait returns the calls made after the n-th, once one of them is method.
func (f *fakeTelegram) wait(t *testing.T, from int, method string) []call {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := append([]call(nil), f.calls[min(from, len(f.calls)):]...)
		f.mu.Unlock()
		for _, c := range got {
			if c.method == method {
				time.Sleep(50 * time.Millisecond) // let the rest of the handler's calls land
				f.mu.Lock()
				got = append([]call(nil), f.calls[min(from, len(f.calls)):]...)
				f.mu.Unlock()
				return got
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s after call %d: %+v", method, from, f.calls)
	return nil
}

// until waits for the calls after the n-th to satisfy ok.
func (f *fakeTelegram) until(t *testing.T, from int, ok func([]call) bool) []call {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := append([]call(nil), f.calls[min(from, len(f.calls)):]...)
		f.mu.Unlock()
		if ok(got) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("not reached after call %d: %+v", from, f.calls[from:])
	return nil
}

func (f *fakeTelegram) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func find(calls []call, method string) (call, bool) {
	for _, c := range calls {
		if c.method == method {
			return c, true
		}
	}
	return call{}, false
}

func text(c call) string { s, _ := c.body["text"].(string); return s }

// buttons flattens a keyboard: text → callback data, url or web app url.
func buttons(c call) map[string]string {
	out := map[string]string{}
	kb, _ := c.body["reply_markup"].(map[string]any)
	rows, _ := kb["inline_keyboard"].([]any)
	for _, r := range rows {
		for _, b := range r.([]any) {
			m := b.(map[string]any)
			v, _ := m["callback_data"].(string)
			if u, ok := m["url"].(string); ok {
				v = u
			}
			if wa, ok := m["web_app"].(map[string]any); ok {
				v = "webapp:" + wa["url"].(string)
			}
			out[m["text"].(string)] = v
		}
	}
	return out
}

// screens stands in for the bot's screens (tgbot): every message gets a menu back, as it
// does in the bot.
type screens struct{}

func (screens) Started(context.Context, *Client) {}

func (screens) Update(_ context.Context, _ *Client, out *Outbox, up Update) {
	if up.Message == nil {
		return
	}
	chat := up.Message.Chat.ID
	out.Reply(chat, "", 1, func(ctx context.Context, c *Client) error {
		_, err := c.Send(ctx, chat, "menu", nil, false)
		return err
	})
}

type env struct {
	t     *testing.T
	ctx   context.Context
	st    *store.Store
	set   *settings.Settings
	tg    *fakeTelegram
	gate  *Gateway
	now   time.Time
	mu    sync.Mutex
	msgID int64
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// setup starts a gateway on a fake Bot API, with a stand-in for the bot's screens unless
// a test takes them away (gate.SetHandler is not called then); with may change its deps
// before it starts.
func setup(t *testing.T, with ...func(e *env, d *Deps)) *env {
	t.Helper()
	return start(t, screens{}, with...)
}

func start(t *testing.T, h Handler, with ...func(e *env, d *Deps)) *env {
	t.Helper()
	e := &env{t: t, now: time.Unix(1_800_000_000, 0), tg: &fakeTelegram{blocked: map[int64]bool{}}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.ctx = ctx
	var err error
	if e.st, err = storetest.Open(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.st.Close() })
	e.set = settings.New(e.st.Q)
	for k, v := range map[string]any{KeyEnabled: true, KeyToken: "123:test"} {
		if err := settings.Set(ctx, e.set, k, v); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(e.tg)
	t.Cleanup(srv.Close)
	deps := Deps{Store: e.st, Settings: e.set, API: srv.URL, Now: e.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Limits: fast}
	for _, f := range with {
		f(e, &deps)
	}
	e.gate = New(deps)
	if h != nil {
		e.gate.SetHandler(h)
	}
	go e.gate.Run(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !e.gate.Status().Running {
		if time.Now().After(deadline) {
			t.Fatalf("the gateway did not start: %+v", e.gate.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return e
}

// say sends a message from a Telegram user.
func (e *env) say(from int64, s string) {
	e.msgID++
	e.tg.push(Update{Message: &Message{MessageID: e.msgID, From: &User{ID: from, FirstName: "Anna", Username: "anna"}, Chat: Chat{ID: from, Type: "private"}, Text: s}})
}

func (e *env) press(from, msg int64, data string) {
	e.tg.push(Update{CallbackQuery: &CallbackQuery{ID: "cb" + data, From: User{ID: from}, Message: &Message{MessageID: msg, Chat: Chat{ID: from, Type: "private"}}, Data: data}})
}
