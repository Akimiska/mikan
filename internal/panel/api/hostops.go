package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/hostchan"
	"mikan/internal/logbuf"
	"mikan/internal/nodeapi"
	"mikan/internal/textutil"
)

// The logs of the panel and of its nodes, the host's `mikan doctor` and a restart of the
// containers: what an admin does over SSH, and what support needs when the admin gives it
// no shell. The doctor and the restart reach the host through files its systemd units
// watch (internal/hostchan); nothing here runs a command.

// doctorWait is how long the panel waits for a server's report; tests shorten it.
var doctorWait = hostchan.DoctorWait

type logsInput struct {
	Source string `query:"source" default:"panel" maxLength:"24" doc:"Чей лог: panel или node:<id> ноды"`
	Tail   int    `query:"tail" default:"200" minimum:"1" maximum:"2000" doc:"Сколько последних строк"`
}

// LogsView is the last lines of a log, oldest first, with the secrets cut out.
type LogsView struct {
	Source string            `json:"source"`
	Lines  []nodeapi.LogLine `json:"lines"`
}

type logsOutput struct{ Body LogsView }

type hostReportOutput struct{ Body nodeapi.HostReport }

type restartOutput struct{ Body nodeapi.RestartAccepted }

func (h *handlers) registerHostOps() {
	huma.Register(h.api, huma.Operation{OperationID: "get-logs", Method: http.MethodGet, Path: "/api/v1/logs", Summary: "Последние строки лога панели или ноды",
		Description: "source=panel или node:<id>; до 2000 строк, самые новые последними. Токены, ключи, пароли, ссылки подписок и секретные пути вырезаны.",
		Tags:        []string{"diagnostics"}}, h.getLogs)
	huma.Register(h.api, huma.Operation{OperationID: "doctor-server", Method: http.MethodPost, Path: "/api/v1/system/doctor", Summary: "mikan doctor на сервере панели",
		Description: "Хост запускает mikan doctor и отдаёт отчёт (Docker, контейнеры, порты, файрвол, часы, DNS, GitHub, диск, память); ждём до минуты. Нужна команда mikan 0.5.1.1 или новее на сервере: mikan update.",
		Tags:        []string{"settings"}}, h.doctorServer)
	huma.Register(h.api, huma.Operation{OperationID: "doctor-node", Method: http.MethodPost, Path: "/api/v1/nodes/{id}/doctor", Summary: "mikan doctor на сервере ноды",
		Description: "То же для сервера удалённой ноды; для своей ноды панели это сервер панели.",
		Tags:        []string{"node"}}, h.doctorNode)
	huma.Register(h.api, huma.Operation{OperationID: "restart-server", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/system/restart", Summary: "Перезапустить контейнеры сервера панели",
		Description: "Хост перезапускает контейнеры через секунду-две: панель на это время недоступна.",
		Tags:        []string{"settings"}, DefaultStatus: http.StatusAccepted}, h.restartServer)
	huma.Register(h.api, huma.Operation{OperationID: "restart-node", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/nodes/{id}/restart", Summary: "Перезапустить контейнеры сервера ноды",
		Description: "Для своей ноды панели используйте /system/restart: она живёт в одних контейнерах с панелью.",
		Tags:        []string{"node"}, DefaultStatus: http.StatusAccepted}, h.restartNode)
}

// secretWords are what only the panel knows to cut out of its logs: the secret paths.
func (h *handlers) secretWords(ctx context.Context) []string {
	paths, err := h.d.Settings.Paths(ctx)
	if err != nil {
		return nil
	}
	return []string{paths.Admin, paths.Sub}
}

func (h *handlers) getLogs(ctx context.Context, in *logsInput) (*logsOutput, error) {
	out := &logsOutput{Body: LogsView{Source: in.Source, Lines: []nodeapi.LogLine{}}}
	words := h.secretWords(ctx)
	clean := func(l nodeapi.LogLine) nodeapi.LogLine {
		// A node's lines are cut where they are written; the panel does it once more, since
		// a node is a server somebody else may run.
		l.Text = logbuf.RedactWords(logbuf.Redact(l.Text), words)
		return l
	}
	if in.Source == "panel" {
		if h.d.Logs == nil {
			return nil, huma.Error409Conflict("logs_unavailable")
		}
		for _, e := range h.d.Logs.Tail(in.Tail) {
			out.Body.Lines = append(out.Body.Lines, clean(nodeapi.LogLine{At: e.At, Level: e.Level, Text: e.Text}))
		}
		return out, nil
	}
	rest, ok := strings.CutPrefix(in.Source, "node:")
	id, err := strconv.ParseInt(rest, 10, 64)
	if !ok || err != nil || id < 1 {
		return nil, fieldErr("query.source", "bad_source")
	}
	if _, err := h.getNode(ctx, id); err != nil {
		return nil, err
	}
	if h.d.Nodes == nil {
		return nil, huma.Error409Conflict("nodes_disabled")
	}
	logs, err := h.d.Nodes.Logs(ctx, id, in.Tail)
	if err != nil {
		return nil, h.hostNodeError(err, "logs", id)
	}
	for _, l := range logs.Lines[:min(len(logs.Lines), nodeapi.MaxLogTail)] {
		l.Level, l.Text = textutil.Clip(l.Level, 16), textutil.Clip(l.Text, 8<<10)
		out.Body.Lines = append(out.Body.Lines, clean(l))
	}
	return out, nil
}

// hostNodeError turns what a node answered to a host operation into the API's codes.
func (h *handlers) hostNodeError(err error, what string, id int64) error {
	var ne *nodeapi.Error
	var se *nodeapi.StatusError
	switch {
	case errors.As(err, &ne):
		switch ne.Code {
		case nodeapi.CodeDoctorBusy:
			return huma.Error409Conflict("doctor_busy")
		case nodeapi.CodeDoctorTimeout:
			return huma.Error504GatewayTimeout("doctor_timeout")
		case nodeapi.CodeRestartPending:
			return huma.Error409Conflict("restart_pending")
		case nodeapi.CodeHostUnavailable:
			return huma.Error409Conflict("host_unavailable")
		}
	case errors.As(err, &se) && se.Status == http.StatusNotFound:
		return huma.Error409Conflict("node_too_old")
	}
	h.d.Log.Warn("node host operation", "op", what, "node", id, "err", err)
	return huma.Error502BadGateway("node_no_answer")
}

// hostError turns the refusals of the panel's own server's channel into the API's codes.
func (h *handlers) hostError(err error, what string) error {
	switch {
	case errors.Is(err, hostchan.ErrBusy):
		return huma.Error409Conflict("doctor_busy")
	case errors.Is(err, hostchan.ErrTimeout):
		return huma.Error504GatewayTimeout("doctor_timeout")
	case errors.Is(err, hostchan.ErrPending):
		return huma.Error409Conflict("restart_pending")
	case errors.Is(err, hostchan.ErrUnavailable):
		return huma.Error409Conflict("host_unavailable")
	}
	h.d.Log.Error("host operation", "op", what, "err", err)
	return huma.Error500InternalServerError("host_failed")
}

func (h *handlers) doctorServer(ctx context.Context, _ *struct{}) (*hostReportOutput, error) {
	h.audit(ctx, sessionOf(ctx).AdminID, "system.doctor", "", "", nil)
	rep, err := h.d.Host.Doctor(ctx, doctorWait)
	if err != nil {
		return nil, h.hostError(err, "doctor")
	}
	return &hostReportOutput{Body: rep}, nil
}

func (h *handlers) doctorNode(ctx context.Context, in *nodeIDInput) (*hostReportOutput, error) {
	n, err := h.getNode(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "node.doctor", "node", strconv.FormatInt(n.ID, 10), map[string]any{"name": n.Name})
	// The panel's own node runs on the panel's server: its doctor is the server's.
	if n.Address == "" {
		rep, err := h.d.Host.Doctor(ctx, doctorWait)
		if err != nil {
			return nil, h.hostError(err, "doctor")
		}
		return &hostReportOutput{Body: rep}, nil
	}
	if h.d.Nodes == nil {
		return nil, huma.Error409Conflict("nodes_disabled")
	}
	rep, err := h.d.Nodes.HostDoctor(ctx, n.ID)
	if err != nil {
		return nil, h.hostNodeError(err, "doctor", n.ID)
	}
	// What a node says is kept within sense, and cut of secrets once more.
	rep, ok := rep.Clean()
	if !ok {
		h.d.Log.Warn("node host operation", "op", "doctor", "node", n.ID, "err", "the report is not one")
		return nil, huma.Error502BadGateway("node_no_answer")
	}
	for i := range rep.Items {
		it := &rep.Items[i]
		it.Label, it.Detail, it.Fix = logbuf.Redact(it.Label), logbuf.Redact(it.Detail), logbuf.Redact(it.Fix)
	}
	return &hostReportOutput{Body: rep}, nil
}

func (h *handlers) restartServer(ctx context.Context, _ *struct{}) (*restartOutput, error) {
	id, err := h.d.Host.Restart()
	if err != nil {
		return nil, h.hostError(err, "restart")
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "system.restart", "", "", map[string]any{"request": id})
	return &restartOutput{Body: nodeapi.RestartAccepted{ID: id}}, nil
}

func (h *handlers) restartNode(ctx context.Context, in *nodeIDInput) (*restartOutput, error) {
	n, err := h.getNode(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if n.Address == "" {
		return nil, huma.Error409Conflict("local_node_restart")
	}
	if h.d.Nodes == nil {
		return nil, huma.Error409Conflict("nodes_disabled")
	}
	acc, err := h.d.Nodes.HostRestart(ctx, n.ID)
	if err != nil {
		return nil, h.hostNodeError(err, "restart", n.ID)
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "node.restart", "node", strconv.FormatInt(n.ID, 10), map[string]any{"name": n.Name, "request": acc.ID})
	// The id is shown: a node's is kept within sense.
	return &restartOutput{Body: nodeapi.RestartAccepted{ID: textutil.Clip(acc.ID, 64)}}, nil
}
