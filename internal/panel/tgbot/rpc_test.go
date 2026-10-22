package tgbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"mikan/internal/panel/model"
)

// The types of the RPC: its interfaces, and the context every method takes and the error it
// may give, which do not cross as values.
var (
	backendType = reflect.TypeFor[Backend]()
	shopType    = reflect.TypeFor[Shop]()
	errorType   = reflect.TypeFor[error]()
	contextType = reflect.TypeFor[context.Context]()
)

// Everything the screens get over the Panel API crosses as JSON whole: no field of the
// arguments and results is unexported or left out of JSON, or the addon would see less
// than the panel's screens do.
func TestRPCTypesCrossWhole(t *testing.T) {
	marshaler := reflect.TypeFor[json.Marshaler]()
	seen := map[reflect.Type]bool{}
	var walk func(path string, tp reflect.Type)
	walk = func(path string, tp reflect.Type) {
		if seen[tp] || tp.Implements(marshaler) || reflect.PointerTo(tp).Implements(marshaler) {
			return
		}
		seen[tp] = true
		switch tp.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(path, tp.Elem())
		case reflect.Map:
			walk(path, tp.Key())
			walk(path, tp.Elem())
		case reflect.Struct:
			for i := range tp.NumField() {
				f := tp.Field(i)
				if !f.IsExported() || f.Tag.Get("json") == "-" {
					t.Errorf("%s: %s.%s does not cross", path, tp, f.Name)
				}
				walk(path, f.Type)
			}
		case reflect.Interface, reflect.Func, reflect.Chan:
			t.Errorf("%s: %s cannot cross", path, tp)
		}
	}
	for _, it := range []reflect.Type{backendType, shopType} {
		methods := []reflect.Type{it}
		if it.Kind() == reflect.Interface {
			methods = methods[:0]
			for i := range it.NumMethod() {
				if m := it.Method(i); m.Name != "Shop" {
					methods = append(methods, m.Type)
				}
			}
		}
		for _, m := range methods {
			for i := range m.NumIn() {
				if m.In(i) != contextType {
					walk(m.String(), m.In(i))
				}
			}
			for i := range m.NumOut() {
				if m.Out(i) != errorType {
					walk(m.String(), m.Out(i))
				}
			}
		}
	}
}

// The addon's side of the RPC against a panel that answers as RPCHandler does: an error of a
// code the model knows comes back as the model's error, so the screens tell it apart with
// errors.Is; any other as an *RPCError with its code and text; the Panel API's refusal of an
// account that is not talking to the bot as ErrNotTalking. A panel that answers what is not
// an answer, or cannot be asked, is an error, not a zero.
func TestRemoteErrors(t *testing.T) {
	ctx := context.Background()
	answer := func(status int, body string) *Remote {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/bot/Owner" || r.Header.Get("Authorization") != "Bearer t" {
				t.Errorf("the call: %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
			}
			if args, _ := io.ReadAll(r.Body); string(args) != "[1]" {
				t.Errorf("the arguments: %s", args)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return NewRemote(srv.URL, "t", nil)
	}
	if tg, err := answer(http.StatusOK, `{"results":[70]}`).Owner(ctx, 1); err != nil || tg != 70 {
		t.Fatalf("Owner: %d %v", tg, err)
	}
	for code, want := range map[string]error{"no_rows": sql.ErrNoRows, "link_limit": model.ErrLinkLimit, "trial_used": model.ErrTrialUsed,
		"promo_unavailable": model.ErrPromo, "deadline_exceeded": context.DeadlineExceeded} {
		if _, err := answer(http.StatusOK, `{"error":{"code":"`+code+`","message":"x"}}`).Owner(ctx, 1); !errors.Is(err, want) {
			t.Errorf("%s came back as %v", code, err)
		}
	}
	var crossed *RPCError
	if _, err := answer(http.StatusOK, `{"error":{"code":"error","message":"something else"}}`).Owner(ctx, 1); !errors.As(err, &crossed) ||
		crossed.Code != "error" || err.Error() != "something else" || errors.Is(err, model.ErrLinkLimit) {
		t.Fatalf("another error: %v", err)
	}
	if _, err := answer(http.StatusForbidden, `{"code":"not_talking","message":"no"}`).Owner(ctx, 1); !errors.Is(err, ErrNotTalking) {
		t.Fatalf("not talking: %v", err)
	}
	for _, bad := range []struct {
		status int
		body   string
	}{
		{http.StatusInternalServerError, `{"code":"internal","message":"x"}`},
		{http.StatusOK, `<html>bad gateway</html>`},
		{http.StatusOK, `{"results":[]}`},
		{http.StatusOK, `{"results":["seventy"]}`},
	} {
		if tg, err := answer(bad.status, bad.body).Owner(ctx, 1); err == nil {
			t.Errorf("%d %s is an answer: %d", bad.status, bad.body, tg)
		}
	}
	if _, err := NewRemote("http://127.0.0.1:1", "t", nil).Owner(ctx, 1); err == nil {
		t.Fatal("an unreachable panel is no error")
	}
}
