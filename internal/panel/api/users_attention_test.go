package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/store/storetest"
)

// state=attention is what the overview's card showed with three lists: out of traffic,
// then expiring, then expired, the rest left out, in one pass over the users.
func TestUserListAttention(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	clock := func() time.Time { return now }
	if err := domain.Seed(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: "admin", PasswordHash: "x", CreatedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	pool := domain.NewPool(st, clock)
	users := domain.NewUsers(st, pool, noChanges{}, clock)
	tariffs, _ := st.Q.ListTariffs(ctx)
	create := func(name string, expires time.Time) int64 {
		t.Helper()
		u, err := users.Create(ctx, domain.CreateInput{Name: name, TariffID: tariffs[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		if !expires.IsZero() {
			if _, err := users.Update(ctx, u.ID, domain.Patch{ExpiresAt: &expires}); err != nil {
				t.Fatal(err)
			}
		}
		return u.ID
	}
	create("active", time.Time{})
	expired := create("expired", now.Add(-time.Hour))
	expiring := create("expiring", now.Add(24*time.Hour))
	limited := create("limited", time.Time{})
	if err := st.Q.AddUserTraffic(ctx, db.AddUserTrafficParams{ID: limited, Down: 1 << 50}); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(st.Q, clock, nil)
	token, _, err := sessions.Create(ctx, 1, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	handler, _, err := New(Deps{
		Version: "test", Store: st, Sessions: sessions, Now: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		IPLimit: auth.NewLimiter(10, time.Minute, time.Minute, time.Hour), UserLimit: auth.NewLimiter(10, time.Minute, time.Minute, time.Hour), TOTP: auth.NewTOTPGuard(),
		Users: users, Pool: pool, Changes: noChanges{},
		SubBase: func(context.Context) string { return "https://203.0.113.10/s" },
		Online:  func() map[string]nodeapi.Online { return map[string]nodeapi.Online{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users?state=attention&limit=5", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var list struct {
		Items []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, it := range list.Items {
		got = append(got, it.ID)
	}
	if want := []int64{limited, expiring, expired}; !slices.Equal(got, want) || list.Total != 3 {
		t.Fatalf("attention = %v (total %d), want %v: %s", got, list.Total, want, rec.Body)
	}
}
