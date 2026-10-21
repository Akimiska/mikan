package botpanel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/model"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgate"
	"mikan/internal/panel/tgbot"
)

type stubBackend struct {
	Backend
	err error
}

func (s stubBackend) Owner(_ context.Context, userID int64) (int64, error)      { return userID * 10, s.err }
func (s stubBackend) Shop() tgbot.Shop                                          { return shop{} }
func (s stubBackend) Member(context.Context, string, int64, bool) (bool, error) { return true, nil }

// rpcCall posts a call to the panel's side of the RPC as Remote does and returns the code and
// the text of the error it is answered with, "" for none: Remote itself calls only the
// methods of the interfaces.
func rpcCall(h http.Handler, method string, args ...any) (code, message string) {
	body, err := json.Marshal(append([]any{}, args...))
	if err != nil {
		return "marshal", err.Error()
	}
	mux := http.NewServeMux()
	mux.Handle("POST /bot/{method}", h)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/bot/"+method, bytes.NewReader(body)))
	var ans struct {
		Error *tgbot.RPCError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		return "unreadable", rec.Body.String()
	}
	if ans.Error == nil {
		return "", ""
	}
	return ans.Error.Code, ans.Error.Message
}

// Errors the screens tell apart come back as themselves; only the interfaces' methods are
// reachable; a panel that cannot be asked is an error, not a zero.
func TestRPCErrorsAndMethods(t *testing.T) {
	serve := func(b tgbot.Backend) *tgbot.Remote {
		mux := http.NewServeMux()
		mux.Handle("POST /bot/{method}", RPCHandler(b))
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return tgbot.NewRemote(srv.URL, "t", nil)
	}
	ctx := context.Background()
	r := serve(stubBackend{})
	if tg, err := r.Owner(ctx, 7); err != nil || tg != 70 {
		t.Fatalf("Owner: %d %v", tg, err)
	}
	if in, err := r.Member(ctx, "@chan", 1, true); err != nil || !in {
		t.Fatalf("Member: %v %v", in, err)
	}
	for _, want := range []error{model.ErrLinkLimit, domain.ErrNotFound, billing.ErrTrialUsed, tgate.ErrLinkExpired} {
		_, err := serve(stubBackend{err: want}).Owner(ctx, 1)
		if !errors.Is(err, want) {
			t.Fatalf("%v came back as %v", want, err)
		}
	}
	other := errors.New("something else")
	if _, err := serve(stubBackend{err: other}).Owner(ctx, 1); err == nil || errors.Is(err, model.ErrLinkLimit) || !strings.Contains(err.Error(), "something else") {
		t.Fatalf("another error: %v", err)
	}
	// Not a method of the interfaces: refused, whatever the backend has.
	h := RPCHandler(stubBackend{})
	for _, m := range []string{"Shop", "Shop.Close", "store", "Owners", "String", "Notices", "NoticeSent", "NoticeDelivered", "PruneNotices", "BroadcastTargets"} {
		if code, msg := rpcCall(h, m); code == "" || !strings.Contains(code, "no_method") && !strings.Contains(msg, "no method") {
			t.Fatalf("%s: %q %q", m, code, msg)
		}
	}
	if code, _ := rpcCall(h, "Owner", 1, 2); code == "" {
		t.Fatal("wrong arguments passed")
	}
	down := tgbot.NewRemote("http://127.0.0.1:1", "t", nil)
	if _, err := down.Owner(ctx, 1); err == nil {
		t.Fatal("an unreachable panel is no error")
	}
}

// Every code the bot knew before the screens and the panel's side were split comes back as
// the error it named, the panel's own (model.ErrorOf), and that error crosses with that code
// (model.CodeOf): an installed bot tells them apart as it did. The table is the old bot's.
func TestOldErrorCodes(t *testing.T) {
	for _, c := range []struct {
		code string
		err  error
	}{
		{"no_rows", sql.ErrNoRows},
		{"not_found", domain.ErrNotFound},
		{"link_limit", model.ErrLinkLimit},
		{"not_channel", model.ErrNotChannel},
		{"link_expired", tgate.ErrLinkExpired},
		{"link_invalid", tgate.ErrLinkInvalid},
		{"unbind_off", domain.ErrUnbindOff},
		{"unbind_cooldown", domain.ErrUnbindCooldown},
		{"reissue_off", domain.ErrReissueOff},
		{"reissue_cooldown", domain.ErrReissueCooldown},
		{"free_active", billing.ErrFreeActive},
		{"free_channel", billing.ErrFreeChannel},
		{"free_off", billing.ErrFreeOff},
		{"not_for_sale", billing.ErrNotForSale},
		{"not_yours", billing.ErrNotYours},
		{"too_many", billing.ErrTooMany},
		{"too_many_subs", billing.ErrTooManySubs},
		{"trial_off", billing.ErrTrialOff},
		{"trial_used", billing.ErrTrialUsed},
		{"balance_off", billing.ErrBalanceOff},
		{"balance_low", billing.ErrBalanceLow},
		{"topup_amount", billing.ErrTopupAmount},
		{"topup_many", billing.ErrTopupMany},
		{"topup_none", billing.ErrTopupNone},
		{"context_canceled", context.Canceled},
		{"deadline_exceeded", context.DeadlineExceeded},
	} {
		if got := model.ErrorOf(c.code); got != c.err {
			t.Errorf("%s comes back as %v, want %v", c.code, got, c.err)
		}
		if got := model.CodeOf(c.err); got != c.code {
			t.Errorf("%v crosses as %q, want %q", c.err, got, c.code)
		}
	}
}

// A subscription with traffic packages left gets them through the RPC as the bot addon asks,
// Remote to RPCHandler to Local: what is left of the main traffic's and of a group's. This
// failed on the code before model.GrantsLeft: the answer was domain.GrantsLeft, a map keyed
// by [2]int64 that encoding/json cannot encode, so the call never had an answer and the
// addon never saw a package.
func TestGrantsLeftOverRPC(t *testing.T) {
	e := setup(t)
	wl, err := e.st.Q.CreateTrafficPool(e.ctx, db.CreateTrafficPoolParams{Name: "WL", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []domain.GrantInput{
		{Bytes: 5 << 30, Lifetime: domain.LifetimeUsed},
		{PoolID: wl.ID, Bytes: 3 << 30, Lifetime: domain.LifetimeUsed},
	} {
		if _, err := e.users.Grant(e.ctx, e.user.ID, g); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.backend.GrantsLeft(e.ctx, e.user.ID, e.clock())
	if err != nil || got.Main != 5<<30 || got.Pools[wl.ID] != 3<<30 || len(got.Pools) != 1 {
		t.Fatalf("the packages left, over the RPC: %+v %v", got, err)
	}
}

// unencodable answers what JSON cannot hold: a time after the year 9999.
type unencodable struct{ tgbot.Backend }

func (unencodable) UnbindState(context.Context, int64) (model.UnbindState, error) {
	return model.UnbindState{Next: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}

// An answer that does not encode reaches the addon as an error, not as an answer cut short:
// a method's results come whole or not at all.
func TestAnswerThatDoesNotEncode(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("POST /bot/{method}", RPCHandler(unencodable{}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	st, err := tgbot.NewRemote(srv.URL, "t", nil).UnbindState(context.Background(), 1)
	var crossed *tgbot.RPCError
	if !errors.As(err, &crossed) || crossed.Code != "error" || !strings.Contains(crossed.Message, "does not encode") || !st.Next.IsZero() {
		t.Fatalf("an answer that does not encode: %+v %v", st, err)
	}
}
