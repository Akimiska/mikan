package httpjson

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeKeepsToTheLimit(t *testing.T) {
	var v struct{ A string }
	if err := Decode(strings.NewReader(`{"A":"x"}`), 9, &v); err != nil || v.A != "x" {
		t.Fatalf("at the limit: %v %+v", err, v)
	}
	if err := Decode(strings.NewReader(`{"A":"xy"}`), 9, &v); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("past the limit: %v", err)
	}
	if err := Decode(strings.NewReader(`{"A":`), 100, &v); err == nil {
		t.Fatal("a cut answer was read")
	}
}

func TestDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			if r.Header.Get("Accept") != "application/json" {
				t.Errorf("accept: %q", r.Header.Get("Accept"))
			}
			_, _ = w.Write([]byte(`{"n":7}`))
		case "/raw":
			_, _ = w.Write([]byte(`{"n":7}`))
		case "/limited":
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"slow down"}`))
		case "/big":
			_, _ = w.Write([]byte(`{"n":` + strings.Repeat("1", 100) + `}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	var v struct{ N int }
	if err := Get(ctx, srv.Client(), srv.URL+"/ok", 1<<10, &v); err != nil || v.N != 7 {
		t.Fatalf("ok: %v %+v", err, v)
	}
	err := Get(ctx, srv.Client(), srv.URL+"/limited", 1<<10, &v)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusTooManyRequests || string(se.Body) != `{"error":"slow down"}` || Code(err) != 429 {
		t.Fatalf("a refusal: %v", err)
	}
	if err := Get(ctx, srv.Client(), srv.URL+"/big", 50, &v); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a large answer: %v", err)
	}
	if Code(errors.New("other")) != 0 {
		t.Fatal("a code from an error of another kind")
	}
	if b, err := Fetch(srv.Client(), mustReq(t, srv.URL+"/raw"), 7); err != nil || string(b) != `{"n":7}` {
		t.Fatalf("fetch: %q %v", b, err)
	}
	if _, err := Fetch(srv.Client(), mustReq(t, srv.URL+"/raw"), 6); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("fetch past the limit: %v", err)
	}
	if err := Get(ctx, srv.Client(), "http://127.0.0.1:1/", 10, &v); !Unreachable(err) || Code(err) != 0 {
		t.Fatalf("no answer: %v", err)
	}
	if Unreachable(&StatusError{Code: 500}) {
		t.Fatal("a status is not unreachable")
	}
}

func mustReq(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
