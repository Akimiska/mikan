package node

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mikan/internal/hostchan"
	"mikan/internal/nodeapi"
)

// The support terminal of a node's server: the panel opens the stream, this passes it to the
// host's agent (internal/hostchan) and back. The node decides nothing about the session: the
// panel checked the grant, the agent ends the session at the grant's end. What this adds is
// that only the panel reaches it (the API's own access), a request of the right shape, and
// that the stream is closed when either side goes.

// terminalHandler is POST /v1/terminal with Upgrade: mikan-terminal; the grant, target and
// the end of the grant (unix seconds) are in the query.
func terminalHandler(e *Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), nodeapi.TerminalProtocol) {
			writeJSON(w, http.StatusBadRequest, nodeapi.Error{Code: "bad_request", Message: "Upgrade: " + nodeapi.TerminalProtocol + " is expected"})
			return
		}
		q := r.URL.Query()
		grant, err1 := strconv.ParseInt(q.Get("grant"), 10, 64)
		expires, err2 := strconv.ParseInt(q.Get("expires"), 10, 64)
		req := hostchan.TerminalRequest{GrantID: grant, Target: q.Get("target"), ExpiresAt: time.Unix(expires, 0)}
		if err := errors.Join(err1, err2); err != nil || req.Check(time.Now()) != nil {
			writeJSON(w, http.StatusBadRequest, nodeapi.Error{Code: "bad_request", Message: "grant, target and expires are not a request for a terminal"})
			return
		}
		host, err := e.hostChan().OpenTerminal(r.Context(), req)
		switch {
		case errors.Is(err, hostchan.ErrTerminalBusy):
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: nodeapi.CodeTerminalBusy, Message: "a terminal session is open on this server"})
			return
		case errors.Is(err, hostchan.ErrTerminalTimeout):
			writeJSON(w, http.StatusGatewayTimeout, nodeapi.Error{Code: nodeapi.CodeTerminalTimeout, Message: "the host did not answer: run mikan update on the server"})
			return
		case errors.Is(err, hostchan.ErrUnavailable):
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: nodeapi.CodeHostUnavailable, Message: "this node has no directory its host watches"})
			return
		case err != nil:
			e.log.Error("host terminal", "err", err)
			writeJSON(w, http.StatusInternalServerError, nodeapi.Error{Code: "terminal_failed", Message: "the session could not be opened"})
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			host.Close()
			writeJSON(w, http.StatusInternalServerError, nodeapi.Error{Code: "terminal_unsupported"})
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			host.Close()
			return
		}
		// A session lasts as long as the grant: no deadlines from here on.
		_ = conn.SetDeadline(time.Time{})
		if _, err := conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + nodeapi.TerminalProtocol + "\r\n\r\n")); err != nil {
			conn.Close()
			host.Close()
			return
		}
		e.log.Info("support terminal opened", "grant", grant, "target", req.Target)
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(host, buf.Reader); done <- struct{}{} }()
		go func() { _, _ = io.Copy(conn, host); done <- struct{}{} }()
		<-done
		// One side went: both do, and the agent ends the session on its closed socket.
		conn.Close()
		host.Close()
		<-done
		e.log.Info("support terminal closed", "grant", grant, "target", req.Target)
	}
}

func recordingsHandler(e *Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := e.hostChan().Recordings()
		switch {
		case errors.Is(err, hostchan.ErrUnavailable):
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: nodeapi.CodeHostUnavailable, Message: "this node has no directory its host watches"})
		case err != nil:
			e.log.Error("host recordings", "err", err)
			writeJSON(w, http.StatusInternalServerError, nodeapi.Error{Code: "recordings_failed", Message: "the recordings could not be listed"})
		default:
			writeJSON(w, http.StatusOK, nodeapi.RecordingList{Recordings: list})
		}
	}
}

func recordingHandler(e *Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, size, err := e.hostChan().OpenRecording(r.PathValue("name"))
		switch {
		case errors.Is(err, hostchan.ErrNoRecording):
			writeJSON(w, http.StatusNotFound, nodeapi.Error{Code: nodeapi.CodeNoRecording, Message: "no such recording"})
			return
		case errors.Is(err, hostchan.ErrUnavailable):
			writeJSON(w, http.StatusConflict, nodeapi.Error{Code: nodeapi.CodeHostUnavailable, Message: "this node has no directory its host watches"})
			return
		case err != nil:
			e.log.Error("host recording", "err", err)
			writeJSON(w, http.StatusInternalServerError, nodeapi.Error{Code: "recording_failed", Message: "the recording could not be read"})
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/x-asciicast")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		_, _ = io.Copy(w, io.LimitReader(f, size))
	}
}
