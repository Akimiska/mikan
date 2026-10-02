package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/nodesync"
)

// Metrics in the Prometheus text format, for Prometheus, VictoriaMetrics, Grafana Agent
// and the like. Scrape with a read API key:
//
//	authorization: { type: Bearer, credentials: mk_… }
//
// Labels stay few: per node and per inbound, never per user, so the series do not grow
// with the users.

type metricsOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

func (h *handlers) registerMetrics() {
	huma.Register(h.api, huma.Operation{OperationID: "metrics", Method: http.MethodGet, Path: "/api/v1/metrics", Summary: "Метрики в формате Prometheus",
		Description: "Пользователи по состояниям, онлайн, трафик, ноды и подключения. Для Prometheus и Grafana: ключ на чтение в заголовке Authorization: Bearer.",
		Tags:        []string{"stats"},
		Responses:   map[string]*huma.Response{"200": {Description: "Prometheus text format 0.0.4", Content: map[string]*huma.MediaType{"text/plain": {}}}},
	}, h.metrics)
}

func (h *handlers) metrics(ctx context.Context, _ *struct{}) (*metricsOutput, error) {
	var m promWriter
	m.gauge("mikan_info", "The panel's version.", 1, "version", h.d.Version)

	now := h.d.Now()
	users, err := h.d.Store.Q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	grants, err := domain.LoadGrantsLeft(ctx, h.d.Store.Q, now)
	if err != nil {
		return nil, err
	}
	states := map[string]int{domain.StateActive: 0, domain.StateExpiring: 0, domain.StateLimited: 0, domain.StateExpired: 0, domain.StateDisabled: 0}
	var up, down int64
	for _, u := range users {
		states[domain.State(u, grants.Main(u.ID), now)]++
		up += u.TotalUp
		down += u.TotalDown
	}
	m.help("mikan_users", "Users by state.", "gauge")
	for _, s := range []string{domain.StateActive, domain.StateExpiring, domain.StateLimited, domain.StateExpired, domain.StateDisabled} {
		m.sample("mikan_users", float64(states[s]), "state", s)
	}
	m.help("mikan_traffic_bytes_total", "Traffic of all users, all time. Drops when a user is deleted.", "counter")
	m.sample("mikan_traffic_bytes_total", float64(up), "direction", "up")
	m.sample("mikan_traffic_bytes_total", float64(down), "direction", "down")

	if h.d.Online != nil {
		slots, err := h.allUserSlots(ctx)
		if err != nil {
			return nil, err
		}
		online := h.d.Online()
		n := 0
		for _, names := range slots {
			if slices.ContainsFunc(names, func(s string) bool { _, ok := online[s]; return ok }) {
				n++
			}
		}
		m.gauge("mikan_users_online", "Users with a connection now.", float64(n))
	}
	if h.d.Pool != nil {
		ps, err := h.d.Pool.Stats(ctx)
		if err != nil {
			return nil, err
		}
		m.help("mikan_slots", "Key slots by state.", "gauge")
		m.sample("mikan_slots", float64(ps.Free), "state", "free")
		m.sample("mikan_slots", float64(ps.Assigned), "state", "assigned")
	}

	nodes, err := h.d.Store.Q.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	type liveNode struct {
		id, name string
		hv       nodesync.HealthView
	}
	var live []liveNode
	m.help("mikan_node_up", "1 when the panel reaches the node and its core runs.", "gauge")
	for _, n := range nodes {
		if n.Enabled == 0 {
			continue
		}
		id := strconv.FormatInt(n.ID, 10)
		ok := false
		if h.d.Nodes != nil {
			if hv, found := h.d.Nodes.Health(n.ID); found && hv.OK {
				ok = true
				live = append(live, liveNode{id, n.Name, hv})
			}
		}
		m.sample("mikan_node_up", b2f(ok), "node_id", id, "node", n.Name)
	}
	if len(live) > 0 {
		for _, f := range []struct {
			name, help string
			v          func(nodesync.HealthView) float64
		}{
			{"mikan_node_connections", "Open connections on the node.", func(hv nodesync.HealthView) float64 { return float64(hv.Health.Conns) }},
			{"mikan_node_cpu_percent", "CPU use of the node's server.", func(hv nodesync.HealthView) float64 { return hv.Health.System.CPUPercent }},
			{"mikan_node_memory_used_bytes", "Memory used on the node's server.", func(hv nodesync.HealthView) float64 { return float64(hv.Health.System.MemUsed) }},
			{"mikan_node_memory_total_bytes", "Memory of the node's server.", func(hv nodesync.HealthView) float64 { return float64(hv.Health.System.MemTotal) }},
			{"mikan_node_receive_bytes_per_second", "Network in on the node's server.", func(hv nodesync.HealthView) float64 { return float64(hv.Health.System.NetRxBps) }},
			{"mikan_node_transmit_bytes_per_second", "Network out on the node's server.", func(hv nodesync.HealthView) float64 { return float64(hv.Health.System.NetTxBps) }},
		} {
			m.help(f.name, f.help, "gauge")
			for _, n := range live {
				m.sample(f.name, f.v(n.hv), "node_id", n.id, "node", n.name)
			}
		}
		m.help("mikan_inbound_up", "1 when the inbound listens on its node.", "gauge")
		for _, n := range live {
			for _, l := range n.hv.Listeners {
				m.sample("mikan_inbound_up", b2f(l.OK), "node_id", n.id, "node", n.name, "inbound", l.Name)
			}
		}
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.gauge("go_goroutines", "Goroutines of the panel.", float64(runtime.NumGoroutine()))
	m.gauge("go_memstats_heap_alloc_bytes", "Heap in use by the panel.", float64(ms.HeapAlloc))
	return &metricsOutput{ContentType: "text/plain; version=0.0.4; charset=utf-8", Body: m.Bytes()}, nil
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// promWriter writes the Prometheus text exposition format.
type promWriter struct{ bytes.Buffer }

func (w *promWriter) help(name, help, typ string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func (w *promWriter) gauge(name, help string, v float64, labels ...string) {
	w.help(name, help, "gauge")
	w.sample(name, v, labels...)
}

// sample writes one line; labels come in name, value pairs.
func (w *promWriter) sample(name string, v float64, labels ...string) {
	w.WriteString(name)
	if len(labels) > 0 {
		w.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				w.WriteByte(',')
			}
			w.WriteString(labels[i])
			w.WriteString(`="`)
			w.WriteString(labelEscaper.Replace(labels[i+1]))
			w.WriteByte('"')
		}
		w.WriteByte('}')
	}
	w.WriteByte(' ')
	w.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	w.WriteByte('\n')
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
