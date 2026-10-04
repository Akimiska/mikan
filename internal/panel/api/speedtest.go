package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/store/db"
)

// The speed test of a node's own way to the internet: run on the admin's word, the
// latest hundred kept per node.

type SpeedTestView struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	PingMs   float64   `json:"ping_ms" doc:"Медиана задержки, мс; -1 — ответов не было"`
	JitterMs float64   `json:"jitter_ms" doc:"Средний разброс задержки, мс"`
	LossPct  float64   `json:"loss_pct" doc:"Потери, %"`
	DownBps  int64     `json:"down_bps" doc:"Загрузка, бит/с"`
	UpBps    int64     `json:"up_bps" doc:"Отдача, бит/с"`
	Error    string    `json:"error,omitempty" doc:"Где тест оборвался; измеренное до того сохранено"`
}

type speedTestOutput struct{ Body SpeedTestView }
type speedTestsOutput struct{ Body []SpeedTestView }

type speedTestsInput struct {
	ID    int64 `path:"id" minimum:"1"`
	Limit int64 `query:"limit" minimum:"1" maximum:"100" default:"30"`
}

func (h *handlers) registerSpeedTests() {
	tags := []string{"node"}
	huma.Register(h.api, huma.Operation{OperationID: "run-node-speedtest", Method: http.MethodPost, Path: "/api/v1/nodes/{id}/speedtest", Summary: "Проверить скорость ноды",
		Description: "Задержка, разброс и потери по 20 DNS-запросам к 1.1.1.1, затем загрузка и отдача через speed.cloudflare.com, по 8 секунд. Занимает около 20 секунд и тратит десятки мегабайт трафика ноды; одновременно на ноде идёт один тест.",
		Tags:        tags}, h.runSpeedTest)
	huma.Register(h.api, huma.Operation{OperationID: "list-node-speedtests", Method: http.MethodGet, Path: "/api/v1/nodes/{id}/speedtests", Summary: "История проверок скорости ноды", Description: "Новые сверху; хранятся последние 100.", Tags: tags}, h.listSpeedTests)
}

func speedTestView(r db.NodeSpeedtest) SpeedTestView {
	return SpeedTestView{ID: r.ID, At: time.Unix(r.At, 0).UTC(), PingMs: r.PingMs, JitterMs: r.JitterMs, LossPct: r.LossPct,
		DownBps: r.DownBps, UpBps: r.UpBps, Error: r.Error}
}

func (h *handlers) runSpeedTest(ctx context.Context, in *nodeIDInput) (*speedTestOutput, error) {
	if _, err := h.getNode(ctx, in.ID); err != nil {
		return nil, err
	}
	if h.d.Nodes == nil {
		return nil, huma.Error502BadGateway("node_unavailable")
	}
	res, err := h.d.Nodes.SpeedTest(ctx, in.ID)
	if err != nil {
		var ne *nodeapi.Error
		switch {
		case errors.As(err, &ne) && ne.Code == "speed_test_busy":
			return nil, huma.Error409Conflict("speed_test_busy")
		case strings.Contains(err.Error(), "status 404"):
			return nil, huma.Error409Conflict("node_too_old")
		}
		h.d.Log.Warn("node speed test", "node", in.ID, "err", err)
		return nil, huma.Error502BadGateway("node_unavailable")
	}
	// What a node says is kept within sense: it is a server somebody else may run.
	sane := func(v, hi float64) float64 {
		if math.IsNaN(v) || v < -1 {
			return -1
		}
		return min(v, hi)
	}
	at := res.At.Unix()
	if res.At.IsZero() || math.Abs(float64(at-h.d.Now().Unix())) > 3600 {
		at = h.d.Now().Unix()
	}
	var row db.NodeSpeedtest
	err = h.d.Store.Tx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.AddNodeSpeedTest(ctx, db.AddNodeSpeedTestParams{NodeID: in.ID, At: at,
			PingMs: sane(res.PingMs, 60_000), JitterMs: sane(res.JitterMs, 60_000), LossPct: math.Max(0, sane(res.LossPct, 100)),
			DownBps: max(0, min(res.DownBps, 1e12)), UpBps: max(0, min(res.UpBps, 1e12)), Error: clipText(res.Error, 300)})
		if err != nil {
			return err
		}
		return q.PruneNodeSpeedTests(ctx, in.ID)
	})
	if err != nil {
		return nil, err
	}
	return &speedTestOutput{Body: speedTestView(row)}, nil
}

func (h *handlers) listSpeedTests(ctx context.Context, in *speedTestsInput) (*speedTestsOutput, error) {
	if _, err := h.getNode(ctx, in.ID); err != nil {
		return nil, err
	}
	rows, err := h.d.Store.Q.ListNodeSpeedTests(ctx, db.ListNodeSpeedTestsParams{NodeID: in.ID, Limit: int32(in.Limit)})
	if err != nil {
		return nil, err
	}
	out := &speedTestsOutput{Body: make([]SpeedTestView, 0, len(rows))}
	for _, r := range rows {
		out.Body = append(out.Body, speedTestView(r))
	}
	return out, nil
}

// clipText cuts s to at most n bytes without splitting a letter.
func clipText(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "")
}
