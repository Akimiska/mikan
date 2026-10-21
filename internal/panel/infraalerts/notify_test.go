package infraalerts

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"mikan/internal/panel/settings"
	"mikan/internal/tgapi"
)

// noticeBot is the alerts' bot with the admin's chat 777 connected, sending to the test's API.
type noticeBot struct{ client *tgapi.Client }

func (b *noticeBot) PolledLang(context.Context) string          { return "ru" }
func (b *noticeBot) PolledInfrastructure(context.Context) bool  { return true }
func (b *noticeBot) InfrastructureEnabled(context.Context) bool { return true }
func (b *noticeBot) InfrastructureAdminChat(context.Context) (int64, bool, error) {
	return 777, true, nil
}
func (b *noticeBot) InfrastructureClient(context.Context) (*tgapi.Client, error) {
	return b.client, nil
}

// A notice goes to the admin's chat at once, with no round of the monitor and whatever the
// alerts' own switches say, and notices without a key are a message each: one queued while
// another is sent does not wait for the next round either.
func TestNotifySendsAtOnce(t *testing.T) {
	st, ctx := testMonitorStore(t)
	var mu sync.Mutex
	var sent []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		mu.Lock()
		sent = append(sent, strconv.FormatInt(msg.ChatID, 10)+" "+msg.Text)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(api.Close)
	now := time.Unix(1_800_000_000, 0)
	m := New(st, settings.New(st.Q), nil, nil, nil, nil, &noticeBot{tgapi.NewClient(api.URL, "1:x", nil)},
		slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })

	m.Notify(ctx, "", "came in")
	m.Notify(ctx, "", "came in")
	m.Notify(ctx, "topup/1", "a top-up")
	m.delivering.Wait()
	mu.Lock()
	got := slices.Clone(sent)
	mu.Unlock()
	slices.Sort(got)
	if want := []string{"777 a top-up", "777 came in", "777 came in"}; !slices.Equal(got, want) {
		t.Fatalf("sent %q, want %q", got, want)
	}
	if left, err := m.load(ctx); err != nil || len(left.Pending) != 0 {
		t.Fatalf("the queue after: %+v %v", left.Pending, err)
	}
}
