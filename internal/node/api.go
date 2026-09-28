package node

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"mikan/internal/nodeapi"
)

// Handler exposes the Node API. It is served on a unix socket only; the socket file
// permissions are the access control.
func Handler(e *Engine, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/state", func(w http.ResponseWriter, r *http.Request) {
		var st nodeapi.DesiredState
		if !decode(w, r, &st) {
			return
		}
		res, err := e.Apply(st)
		if err != nil {
			fail(w, log, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("PUT /v1/policies", func(w http.ResponseWriter, r *http.Request) {
		var req nodeapi.PoliciesRequest
		if !decode(w, r, &req) {
			return
		}
		e.SetPolicies(req)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/kick", func(w http.ResponseWriter, r *http.Request) {
		var req nodeapi.KickRequest
		if !decode(w, r, &req) {
			return
		}
		e.Reg.Kick(req.Slots)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/counters", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, e.Reg.Counters())
	})
	mux.HandleFunc("POST /v1/counters/ack", func(w http.ResponseWriter, r *http.Request) {
		var req nodeapi.AckRequest
		if !decode(w, r, &req) {
			return
		}
		if !e.Reg.Ack(req.Epoch, req.Seq) {
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: "stale_ack", Message: "no outstanding batch with this epoch/seq"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, e.Health())
	})
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		var since time.Time
		if s := r.URL.Query().Get("since"); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, nodeapi.Error{Code: "bad_request", Message: "since must be RFC 3339"})
				return
			}
			since = t
		}
		writeJSON(w, http.StatusOK, e.Logs(since))
	})
	return mux
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, nodeapi.Error{Code: "bad_request", Message: err.Error()})
		return false
	}
	return true
}

func fail(w http.ResponseWriter, log *slog.Logger, err error) {
	var ne *nodeapi.Error
	if errors.As(err, &ne) {
		status := http.StatusInternalServerError
		if ne.Code == "invalid_state" {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, ne)
		return
	}
	log.Error("node api", "err", err)
	writeJSON(w, http.StatusInternalServerError, nodeapi.Error{Code: "apply_failed", Message: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
