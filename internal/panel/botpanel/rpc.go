package botpanel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/model"
	"mikan/internal/panel/tgbot"
)

// The bot's screens as an addon see the panel over the Panel API: POST /panel/v2/bot/<method>
// calls one method of tgbot.Backend or tgbot.Shop ("Shop.<method>") with its arguments as a
// JSON array (the context left out) and answers its results the same way, or an error by its
// code (model.CodeOf). RPCHandler is the panel's side, tgbot.Remote the addon's.

var (
	backendType = reflect.TypeFor[tgbot.Backend]()
	shopType    = reflect.TypeFor[tgbot.Shop]()
	errorType   = reflect.TypeFor[error]()
	contextType = reflect.TypeFor[context.Context]()
)

// RPCHandler serves the calls of tgbot.Remote on b: only the methods of tgbot.Backend and
// tgbot.Shop are reachable, whatever else b has (Local's notices and broadcast targets are the
// panel's).
func RPCHandler(b tgbot.Backend) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("method")
		var fn reflect.Value
		switch {
		case strings.HasPrefix(name, "Shop."):
			if m, ok := shopType.MethodByName(strings.TrimPrefix(name, "Shop.")); ok {
				fn = reflect.ValueOf(b.Shop()).MethodByName(m.Name)
			}
		case name != "Shop":
			if m, ok := backendType.MethodByName(name); ok {
				fn = reflect.ValueOf(b).MethodByName(m.Name)
			}
		}
		if !fn.IsValid() {
			rpcWrite(w, http.StatusNotFound, map[string]any{"error": tgbot.RPCError{Code: "no_method", Message: "no method " + name}})
			return
		}
		var raw []json.RawMessage
		if err := json.NewDecoder(io.LimitReader(r.Body, tgbot.RPCMaxBody)).Decode(&raw); err != nil {
			rpcWrite(w, http.StatusBadRequest, map[string]any{"error": tgbot.RPCError{Code: "bad_request", Message: "the body is a JSON array of the arguments"}})
			return
		}
		t := fn.Type()
		args := []reflect.Value{}
		first := 0
		if t.NumIn() > 0 && t.In(0) == contextType {
			args, first = append(args, reflect.ValueOf(r.Context())), 1
		}
		if len(raw) != t.NumIn()-first {
			rpcWrite(w, http.StatusBadRequest, map[string]any{"error": tgbot.RPCError{Code: "bad_request", Message: fmt.Sprintf("%s takes %d arguments", name, t.NumIn()-first)}})
			return
		}
		for i, a := range raw {
			v := reflect.New(t.In(first + i))
			if err := json.Unmarshal(a, v.Interface()); err != nil {
				rpcWrite(w, http.StatusBadRequest, map[string]any{"error": tgbot.RPCError{Code: "bad_request", Message: fmt.Sprintf("argument %d: %v", i+1, err)}})
				return
			}
			args = append(args, v.Elem())
		}
		out := fn.Call(args)
		results := []any{}
		for i, v := range out {
			if t.Out(i) == errorType {
				if !v.IsNil() {
					rpcWrite(w, http.StatusOK, map[string]any{"error": codeOf(v.Interface().(error))})
					return
				}
				continue
			}
			results = append(results, v.Interface())
		}
		rpcWrite(w, http.StatusOK, map[string]any{"results": results})
	})
}

// codeOf is err as it crosses: its code (model.CodeOf; any refusal of a promo code as one), or
// "error" with its text.
func codeOf(err error) tgbot.RPCError {
	code := model.CodeOf(err)
	if c, known := billing.Refusal(err); code == "" && known && c == model.RefusalPromo {
		code = model.CodeOf(model.ErrPromo)
	}
	if code == "" {
		code = "error"
	}
	return tgbot.RPCError{Code: code, Message: err.Error()}
}

// rpcWrite answers v. An answer that does not encode is an error the addon reads as such, not
// an answer cut short: the method's results reach it whole or not at all.
func rpcWrite(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status = http.StatusOK
		body, _ = json.Marshal(map[string]any{"error": tgbot.RPCError{Code: "error", Message: "the answer does not encode: " + err.Error()}})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
