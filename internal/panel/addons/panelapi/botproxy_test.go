package panelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/tgate"
)

// fakeTelegram is the Bot API behind the gateway: it records what reached it.
type fakeTelegram struct {
	mu    sync.Mutex
	calls []fakeCall
}

type fakeCall struct {
	path, contentType string
	body              []byte
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{r.URL.Path, r.Header.Get("Content-Type"), body})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(r.URL.Path, "/getChat") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
		return
	}
	_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":7}}`)
}

func (f *fakeTelegram) last() fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return fakeCall{}
	}
	return f.calls[len(f.calls)-1]
}

type gateway struct {
	c   *tgate.Client
	out *tgate.Outbox
}

func (g gateway) Client() *tgate.Client { return g.c }
func (g gateway) Outbox() *tgate.Outbox { return g.out }

type recorder struct{ ups []int64 }

func (r *recorder) Started(context.Context, *tgate.Client) {}
func (r *recorder) Update(_ context.Context, _ *tgate.Client, _ *tgate.Outbox, up tgate.Update) error {
	r.ups = append(r.ups, up.UpdateID)
	return nil
}

func proxyWorld(t *testing.T) (*Server, *fakeTelegram) {
	t.Helper()
	s, _ := kvWorld(t)
	tg := &fakeTelegram{}
	srv := httptest.NewServer(tg)
	t.Cleanup(srv.Close)
	c := tgate.NewClient(srv.URL, "123:REAL", nil)
	out := tgate.NewOutbox(c, tgate.Limits{Total: 1000, Bulk: 1000, PerChat: 1000, Burst: 1000, Workers: 2, MaxTries: 3}, time.Now, func(int64) {}, func(error) {})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go out.Run(ctx)
	s.d.Telegram = gateway{c, out}
	s.d.Settings = settings.New(s.d.Store.Q)
	s.d.SubBase = func(context.Context) string { return "https://panel.example:2096/s3cr" }
	return s, tg
}

type tgAnswer struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func bot(s *Server, token, method, ct string, body []byte) (int, tgAnswer) {
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:41990/bot0:"+token+"/"+method, bytes.NewReader(body))
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var a tgAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	return w.Code, a
}

func update(id int64, text string) tgate.Update {
	raw := []byte(`{"update_id":` + itoa64(id) + `,"message":{"message_id":1,"chat":{"id":555,"type":"private"},"text":"` + text + `","photo":[{"file_id":"x"}]}}`)
	var up tgate.Update
	_ = json.Unmarshal(raw, &up)
	return up
}

// The addon that holds the bot reads what Telegram sent, whole, and acknowledges it with its
// offset; without a holder the panel's screens answer; nobody else gets in.
func TestBotSlotAndUpdates(t *testing.T) {
	s, _ := proxyWorld(t)
	ctx := context.Background()
	screens := &recorder{}
	slot := s.BotSlot(screens)

	if err := slot.Update(ctx, nil, nil, update(1, "/start")); err != nil || len(screens.ups) != 1 {
		t.Fatalf("no holder: the screens answer: %v %v", err, screens.ups)
	}
	if code, _ := bot(s, botToken, "getUpdates", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("no holder: the addon is not let in: %d", code)
	}
	// fresh has no bot permission: naming it changes nothing
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(settings.Set(ctx, s.d.Settings, SlotKey, "fresh"))
	if s.Slot(ctx) != "" {
		t.Fatal("an addon without bot holds the bot")
	}
	must(settings.Set(ctx, s.d.Settings, SlotKey, "telegram"))
	if s.Slot(ctx) != "telegram" {
		t.Fatal("the holder is not taken")
	}
	for _, tok := range []string{strings.Repeat("k", 48), newToken, strings.Repeat("z", 48)} {
		if code, _ := bot(s, tok, "getUpdates", "", nil); code != http.StatusUnauthorized {
			t.Fatalf("an addon that does not hold the bot: %d", code)
		}
	}

	must(slot.Update(ctx, nil, nil, update(10, "hi")))
	must(slot.Update(ctx, nil, nil, update(11, "there")))
	if len(screens.ups) != 1 {
		t.Fatal("the screens answered while an addon holds the bot")
	}
	_, a := bot(s, botToken, "getUpdates", "application/json", []byte(`{"offset":0,"timeout":0}`))
	var ups []map[string]any
	if err := json.Unmarshal(a.Result, &ups); err != nil || len(ups) != 2 || ups[0]["message"].(map[string]any)["photo"] == nil {
		t.Fatalf("updates, whole: %s", a.Result)
	}
	// form parameters, as python-telegram-bot sends them
	_, a = bot(s, botToken, "getUpdates", "application/x-www-form-urlencoded", []byte("offset=11&limit=10"))
	if err := json.Unmarshal(a.Result, &ups); err != nil || len(ups) != 1 {
		t.Fatalf("after offset 11: %s", a.Result)
	}
	// a long poll wakes on a new update
	done := make(chan []map[string]any)
	go func() {
		_, a := bot(s, botToken, "getUpdates", "application/json", []byte(`{"offset":12,"timeout":20}`))
		var got []map[string]any
		_ = json.Unmarshal(a.Result, &got)
		done <- got
	}()
	time.Sleep(200 * time.Millisecond)
	must(slot.Update(ctx, nil, nil, update(12, "new")))
	select {
	case got := <-done:
		if len(got) != 1 {
			t.Fatalf("woken with %v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the long poll did not wake")
	}
	// deleteWebhook is a no-op for frameworks; dropping pending updates empties the buffer
	if code, a := bot(s, botToken, "deleteWebhook", "application/json", []byte(`{"drop_pending_updates":true}`)); code != 200 || !a.OK {
		t.Fatalf("deleteWebhook: %d %+v", code, a)
	}
	if n, _ := s.d.Store.Q.CountAddonUpdates(ctx); n != 0 {
		t.Fatalf("%d updates left after dropping", n)
	}
}

// Calls go to Telegram with the panel's token and come back as Telegram answered; what an
// addon may not do is refused as Telegram would; Mini App buttons open only the panel.
func TestBotCalls(t *testing.T) {
	s, tg := proxyWorld(t)
	ctx := context.Background()
	if err := settings.Set(ctx, s.d.Settings, SlotKey, "telegram"); err != nil {
		t.Fatal(err)
	}
	send := `{"chat_id":555,"text":"hi","reply_markup":{"inline_keyboard":[[{"text":"a","callback_data":"x"}]]}}`
	code, a := bot(s, botToken, "sendMessage", "application/json", []byte(send))
	if code != 200 || !a.OK || string(a.Result) != `{"message_id":7}` {
		t.Fatalf("sendMessage: %d %+v", code, a)
	}
	if c := tg.last(); c.path != "/bot123:REAL/sendMessage" || string(c.body) != send {
		t.Fatalf("Telegram got %s %s", c.path, c.body)
	}
	// Telegram's refusal comes back as it is
	if code, a := bot(s, botToken, "getChat", "application/json", []byte(`{"chat_id":1}`)); code != 400 || a.Description != "Bad Request: chat not found" {
		t.Fatalf("a refusal: %d %+v", code, a)
	}
	for _, m := range []string{"setWebhook", "logOut", "close", "createInvoiceLink", "sendInvoice", "answerPreCheckoutQuery", "refundStarPayment", "getUpdatesX"} {
		if code, a := bot(s, botToken, m, "application/json", []byte(`{}`)); code != 403 || a.OK || a.ErrorCode != 403 {
			t.Fatalf("%s: %d %+v", m, code, a)
		}
	}
	if code, _ := bot(s, botToken, "sendMessage", "application/json", []byte(`{"text":"no chat"}`)); code != 400 {
		t.Fatalf("no chat_id: %d", code)
	}
	for _, kb := range []string{
		`{"inline_keyboard":[[{"text":"a","web_app":{"url":"https://evil.example/app"}}]]}`,
		`{"inline_keyboard":[[{"text":"a","login_url":{"url":"https://evil.example/login"}}]]}`,
		`{"keyboard":[[{"text":"a","web_app":{"url":"https://panel.example:2096/s3cr-other/tg"}}]]}`,
	} {
		body, _ := json.Marshal(map[string]any{"chat_id": 555, "text": "x", "reply_markup": json.RawMessage(kb)})
		if code, _ := bot(s, botToken, "sendMessage", "application/json", body); code != 400 {
			t.Fatalf("a foreign Mini App passed: %s %d", kb, code)
		}
	}
	own := `{"chat_id":555,"text":"x","reply_markup":"{\"inline_keyboard\":[[{\"text\":\"a\",\"web_app\":{\"url\":\"https://panel.example:2096/s3cr/tg\"}}]]}"}`
	if code, _ := bot(s, botToken, "sendMessage", "application/json", []byte(own)); code != 200 {
		t.Fatalf("the panel's own Mini App: %d", code)
	}
	if code, _ := bot(s, botToken, "setChatMenuButton", "application/json", []byte(`{"menu_button":{"type":"web_app","text":"x","web_app":{"url":"https://evil.example"}}}`)); code != 400 {
		t.Fatalf("a foreign menu button: %d", code)
	}

	// a document goes up as the addon made it
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("chat_id", "555")
	fw, _ := mw.CreateFormFile("document", "a.txt")
	_, _ = fw.Write([]byte("hello file"))
	_ = mw.Close()
	if code, _ := bot(s, botToken, "sendDocument", mw.FormDataContentType(), buf.Bytes()); code != 200 {
		t.Fatalf("sendDocument: %d", code)
	}
	if c := tg.last(); c.path != "/bot123:REAL/sendDocument" || !bytes.Equal(c.body, buf.Bytes()) || !strings.HasPrefix(c.contentType, "multipart/form-data") {
		t.Fatalf("the document reached Telegram as %s %q", c.contentType, c.path)
	}

	// the bot off: a clear refusal
	s.d.Telegram = gateway{}
	if code, a := bot(s, botToken, "sendMessage", "application/json", []byte(send)); code != 502 || a.OK {
		t.Fatalf("the bot off: %d %+v", code, a)
	}
}
