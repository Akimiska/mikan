package node

import (
	"errors"
	"net/http"
	"strconv"

	"mikan/internal/hostchan"
	"mikan/internal/nodeapi"
)

// The node's logs and the host's doctor and restart, for the panel's Diagnostics page and
// support: the same questions the panel answers about its own server. Only the panel reaches
// the API (the socket's permissions, or its pinned certificate).

// doctorWait is how long the node waits for its host's report; tests shorten it.
var doctorWait = hostchan.DoctorWait

// hostChan is the node's channel to its server's units (internal/hostchan); the data
// directory is what the host watches at data/node.
func (e *Engine) hostChan() *hostchan.Channel {
	e.hostOnce.Do(func() { e.hostCh = hostchan.New(e.dataDir) })
	return e.hostCh
}

func logsHandler(e *Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tail := nodeapi.DefaultLogTail
		if v := r.URL.Query().Get("tail"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				writeJSON(w, http.StatusBadRequest, nodeapi.Error{Code: "bad_request", Message: "tail must be a number from 1 to " + strconv.Itoa(nodeapi.MaxLogTail)})
				return
			}
			tail = min(n, nodeapi.MaxLogTail)
		}
		out := nodeapi.Logs{Lines: []nodeapi.LogLine{}}
		if e.logs != nil {
			for _, l := range e.logs.Tail(tail) {
				out.Lines = append(out.Lines, nodeapi.LogLine{At: l.At, Level: l.Level, Text: l.Text})
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func doctorHandler(e *Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, err := e.hostChan().Doctor(r.Context(), doctorWait)
		switch {
		case errors.Is(err, hostchan.ErrBusy):
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: nodeapi.CodeDoctorBusy, Message: "a doctor run is going on"})
		case errors.Is(err, hostchan.ErrTimeout):
			writeJSON(w, http.StatusGatewayTimeout, nodeapi.Error{Code: nodeapi.CodeDoctorTimeout, Message: "the host did not answer: run mikan update on the server"})
		case errors.Is(err, hostchan.ErrUnavailable):
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: nodeapi.CodeHostUnavailable, Message: "this node has no directory its host watches"})
		case err != nil:
			e.log.Error("host doctor", "err", err)
			writeJSON(w, http.StatusInternalServerError, nodeapi.Error{Code: "doctor_failed", Message: "the request could not be written"})
		default:
			writeJSON(w, http.StatusOK, rep)
		}
	}
}

func restartHandler(e *Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := e.hostChan().Restart()
		switch {
		case errors.Is(err, hostchan.ErrPending):
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: nodeapi.CodeRestartPending, Message: "an earlier restart request has not been taken yet"})
		case errors.Is(err, hostchan.ErrUnavailable):
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: nodeapi.CodeHostUnavailable, Message: "this node has no directory its host watches"})
		case err != nil:
			e.log.Error("host restart", "err", err)
			writeJSON(w, http.StatusInternalServerError, nodeapi.Error{Code: "restart_failed", Message: "the request could not be written"})
		default:
			e.log.Info("restart asked for by the panel", "id", id)
			writeJSON(w, http.StatusAccepted, nodeapi.RestartAccepted{ID: id})
		}
	}
}
