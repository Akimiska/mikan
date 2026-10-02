package tgbackup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metacubex/age"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/tgbot"
)

const password = "correct horse battery"

func open(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, dir
}

// unpack decrypts a backup and returns the archive's entries by name.
func unpack(t *testing.T, file []byte, pass string) map[string][]byte {
	t.Helper()
	id, err := age.NewScryptIdentity(pass)
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(file), id)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = b
	}
}

// The archive is what `mikan restore` takes: data/panel/backup.db, a whole SQLite file
// with the panel's data, and nothing else; without the password it does not open.
func TestMakeIsARestorableEncryptedArchive(t *testing.T) {
	st, dir := open(t)
	ctx := context.Background()
	if err := settings.Set(ctx, settings.New(st.Q), settings.KeyBrand, "Mandarin"); err != nil {
		t.Fatal(err)
	}
	file, err := Make(ctx, st.DB, dir, password, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(file, []byte("age-encryption.org/v1")) {
		t.Fatalf("not an age file: %.30q", file)
	}
	entries := unpack(t, file, password)
	if len(entries) != 3 || entries["data/"] == nil || entries["data/panel/"] == nil {
		t.Fatalf("entries: %v", keys(entries))
	}
	db := entries["data/panel/backup.db"]
	if !bytes.HasPrefix(db, []byte("SQLite format 3\x00")) || !bytes.Contains(db, []byte("Mandarin")) {
		t.Fatalf("the database is not in it (%d bytes)", len(db))
	}
	id, _ := age.NewScryptIdentity("wrong password!!")
	if _, err := age.Decrypt(bytes.NewReader(file), id); err == nil {
		t.Fatal("a wrong password opens the backup")
	}
	// The copy made on the way is gone.
	left, _ := os.ReadDir(dir)
	for _, e := range left {
		if strings.HasPrefix(e.Name(), ".telegram-backup-") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

func keys(m map[string][]byte) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

type fakeBot struct {
	client *tgbot.Client
	chat   int64
}

func (b fakeBot) InfrastructureClient(context.Context) (*tgbot.Client, error) {
	if b.client == nil {
		return nil, tgbot.ErrOff
	}
	return b.client, nil
}

func (b fakeBot) InfrastructureAdminChat(context.Context) (int64, bool, error) {
	return b.chat, b.chat != 0, nil
}

type sent struct {
	chat, name, caption string
	file                []byte
}

// fakeTelegram answers sendDocument and keeps what it got.
func fakeTelegram(t *testing.T) (*tgbot.Client, func() []sent) {
	var mu sync.Mutex
	var got []sent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendDocument") {
			http.NotFound(w, r)
			return
		}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		var s sent
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			switch p.FormName() {
			case "chat_id":
				s.chat = string(b)
			case "caption":
				s.caption = string(b)
			case "document":
				s.name, s.file = p.FileName(), b
			}
		}
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1, "chat": map[string]any{"id": 42}}})
	}))
	t.Cleanup(srv.Close)
	return tgbot.NewClient(srv.URL, "123:abc", nil), func() []sent {
		mu.Lock()
		defer mu.Unlock()
		return append([]sent(nil), got...)
	}
}

func TestSendGoesToTheAdminChat(t *testing.T) {
	st, dir := open(t)
	ctx := context.Background()
	set := settings.New(st.Q)
	client, got := fakeTelegram(t)
	now := time.Date(2026, 10, 2, 3, 30, 0, 0, time.UTC)
	s := New(st.DB, set, fakeBot{client: client, chat: 42}, dir, func(context.Context) string { return "vpn.example.com" }, func() time.Time { return now }, nil)

	if _, err := s.Send(ctx); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("without a password: %v", err)
	}
	if err := settings.Set(ctx, set, KeyPassword, password); err != nil {
		t.Fatal(err)
	}
	stt, err := s.Send(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g := got()
	if len(g) != 1 || g[0].chat != "42" || g[0].name != "mikan-vpn.example.com-20261002-0330.tar.gz.age" {
		t.Fatalf("sent: %+v", g)
	}
	if !strings.Contains(g[0].caption, "mikan restore") || unpack(t, g[0].file, password)["data/panel/backup.db"] == nil {
		t.Fatalf("caption %q", g[0].caption)
	}
	if stt.LastError != "" || stt.LastOKDay != now.Unix()/86400 || stt.LastSize != int64(len(g[0].file)) {
		t.Fatalf("state: %+v", stt)
	}
	saved, _, _ := settings.Get[State](ctx, set, KeyState)
	if saved != stt {
		t.Fatalf("state kept: %+v", saved)
	}

	s.bot = fakeBot{client: client}
	if stt, err := s.Send(ctx); !errors.Is(err, ErrNoChat) || stt.LastError != ErrNoChat.Error() || stt.LastOKDay == 0 {
		t.Fatalf("without a chat: %v %+v", err, stt)
	}
}

// Once a day at the chosen hour; a failed one again the next hour, not every minute.
func TestDue(t *testing.T) {
	st, dir := open(t)
	ctx := context.Background()
	set := settings.New(st.Q)
	now := time.Date(2026, 10, 2, 2, 59, 0, 0, time.UTC)
	s := New(st.DB, set, fakeBot{}, dir, func(context.Context) string { return "" }, func() time.Time { return now }, nil)
	if s.due(ctx) {
		t.Fatal("off by default")
	}
	_ = settings.Set(ctx, set, KeyEnabled, true)
	if s.due(ctx) {
		t.Fatal("before the hour")
	}
	now = now.Add(time.Minute) // 03:00
	if !s.due(ctx) {
		t.Fatal("at the hour")
	}
	_ = settings.Set(ctx, set, KeyState, State{LastTryHour: now.Unix() / 3600})
	if s.due(ctx) {
		t.Fatal("a failed try is not repeated within the hour")
	}
	now = now.Add(time.Hour)
	if !s.due(ctx) {
		t.Fatal("the next hour it is tried again")
	}
	_ = settings.Set(ctx, set, KeyState, State{LastOKDay: now.Unix() / 86400})
	if s.due(ctx) {
		t.Fatal("one a day")
	}
	_ = settings.Set(ctx, set, KeyHour, 23)
	_ = settings.Set(ctx, set, KeyState, State{})
	if s.due(ctx) {
		t.Fatal("an hour of its own")
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"vpn.example.com": "vpn.example.com", "203.0.113.10": "203.0.113.10", "2001:db8::1": "2001-db8--1", "": "panel", "../x": "..x"} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}
