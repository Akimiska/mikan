package nodeapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"mikan/internal/textutil"
)

// What the panel and its nodes can show and do about the server they run on without SSH:
// their own logs, the host's `mikan doctor` and a restart of the containers. The doctor and
// the restart go through files that the host's systemd units watch (internal/hostchan);
// the node API only carries the questions.

// Limits of the log answer: a tail is at most the buffer's size.
const (
	DefaultLogTail = 200
	MaxLogTail     = 2000
)

// LogLine is one line of a process's log, with its secrets already cut out.
type LogLine struct {
	At    time.Time `json:"at"`
	Level string    `json:"level" doc:"DEBUG, INFO, WARN или ERROR"`
	Text  string    `json:"text" doc:"Строка целиком, как её пишет slog, без секретов"`
}

// Logs answers GET /v1/logs?tail=N: the last N lines, oldest first.
type Logs struct {
	Lines []LogLine `json:"lines"`
}

// The levels of a line of the host's report.
const (
	HostOK    = "ok"
	HostWarn  = "warn"
	HostError = "error"
)

// HostItem is one line of `mikan doctor`.
type HostItem struct {
	Level  string `json:"level" enum:"ok,warn,error"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty" doc:"Что сделать; пусто, если всё хорошо"`
}

// HostReport is what `mikan doctor --json` said about the server of a panel or a node:
// Docker, containers, ports, the firewall, the clock, DNS, GitHub and GHCR, disk and memory.
type HostReport struct {
	ID       string     `json:"id" doc:"Заявка, на которую это ответ"`
	At       string     `json:"at" doc:"RFC 3339"`
	Version  string     `json:"version"`
	Kind     string     `json:"kind" enum:"panel,node"`
	Errors   int        `json:"errors"`
	Warnings int        `json:"warnings"`
	Items    []HostItem `json:"items"`
}

// Limits of a report: the host writes a few dozen short lines; the panel keeps and shows
// whatever it reads, so a damaged file must not fill its pages.
const (
	MaxHostItems  = 64
	MaxHostField  = 200
	MaxHostDetail = 1000
)

// Clean returns the report with known levels and fields of a sane length, and the counts
// worked out from the items instead of taken from the file; false when it is no report
// of a panel or a node.
func (r HostReport) Clean() (HostReport, bool) {
	if r.Kind != "panel" && r.Kind != "node" {
		return HostReport{}, false
	}
	r.ID, r.At, r.Version = textutil.Clip(r.ID, 64), textutil.Clip(r.At, 64), textutil.Clip(r.Version, 64)
	items := make([]HostItem, 0, min(len(r.Items), MaxHostItems))
	r.Errors, r.Warnings = 0, 0
	for _, it := range r.Items {
		if len(items) == MaxHostItems {
			break
		}
		switch it.Level {
		case HostOK:
		case HostWarn:
			r.Warnings++
		case HostError:
			r.Errors++
		default:
			it.Level = HostWarn
			r.Warnings++
		}
		it.Label, it.Fix = textutil.Clip(it.Label, MaxHostField), textutil.Clip(it.Fix, MaxHostDetail)
		it.Detail = textutil.Clip(it.Detail, MaxHostDetail)
		items = append(items, it)
	}
	r.Items = items
	return r, true
}

// RestartAccepted answers POST /v1/restart: the request is written, and the host restarts
// the containers a moment later.
type RestartAccepted struct {
	ID string `json:"id"`
}

// Logs asks the node for the last tail lines of its log. Nodes that predate it answer 404
// (a *StatusError).
func (c *Client) Logs(ctx context.Context, tail int) (Logs, error) {
	var r Logs
	q := url.Values{"tail": {strconv.Itoa(tail)}}
	err := c.do(ctx, http.MethodGet, "/v1/logs?"+q.Encode(), nil, &r, 10*time.Second)
	return r, err
}

// HostDoctor has the host of the node run `mikan doctor` and returns its report; it takes
// up to a minute. Nodes that predate it answer 404, a host without the unit that runs it
// *Error{Code: CodeDoctorTimeout}, one that is being asked already CodeDoctorBusy.
func (c *Client) HostDoctor(ctx context.Context) (HostReport, error) {
	var r HostReport
	err := c.do(ctx, http.MethodPost, "/v1/doctor", nil, &r, 75*time.Second)
	return r, err
}

// HostRestart has the host of the node restart its containers, the node's included.
func (c *Client) HostRestart(ctx context.Context) (RestartAccepted, error) {
	var r RestartAccepted
	err := c.do(ctx, http.MethodPost, "/v1/restart", nil, &r, 10*time.Second)
	return r, err
}

// The codes of the host operations of a node.
const (
	// CodeDoctorTimeout: the host did not answer in time: its unit is missing (an older
	// installer: mikan update brings it) or stuck.
	CodeDoctorTimeout = "doctor_timeout"
	// CodeDoctorBusy: a doctor run is going on already.
	CodeDoctorBusy = "doctor_busy"
	// CodeRestartPending: an earlier restart request has not been taken yet.
	CodeRestartPending = "restart_pending"
	// CodeHostUnavailable: the node has no directory the host watches (tests, development).
	CodeHostUnavailable = "host_unavailable"
)
