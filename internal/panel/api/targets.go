package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/settings"
	"mikan/internal/proto"
	"mikan/internal/scan"
)

type checkTargetInput struct {
	Body struct {
		Dest string `json:"dest" maxLength:"255" doc:"host:port"`
		SNI  string `json:"sni,omitempty" maxLength:"253" doc:"Имя для клиентов; по умолчанию — хост из dest"`
	}
}

type checkTargetOutput struct{ Body scan.Result }

type scanTargetsOutput struct {
	Body struct {
		IP        string        `json:"ip" doc:"Адрес сервера, вокруг которого искали"`
		Scanned   int           `json:"scanned"`
		SelfSteal *scan.Result  `json:"self_steal,omitempty" doc:"Свой домен с сертификатом панели"`
		Results   []scan.Result `json:"results"`
	}
}

// scanning allows one neighbor scan at a time: it opens ~250 connections.
var scanning sync.Mutex

func (h *handlers) registerTargets() {
	huma.Register(h.api, huma.Operation{OperationID: "check-target", Method: http.MethodPost, Path: "/api/v1/inbounds/check-target", Summary: "Проверить сайт как цель REALITY", Tags: []string{"inbounds"}}, h.checkTarget)
	huma.Register(h.api, huma.Operation{OperationID: "scan-targets", Method: http.MethodPost, Path: "/api/v1/inbounds/scan-targets", Summary: "Подобрать цели REALITY рядом с сервером", Tags: []string{"inbounds"}}, h.scanTargets)
}

func (h *handlers) panelPort(ctx context.Context) int {
	p, _, _ := settings.Get[int](ctx, h.d.Settings, settings.KeyPanelPort)
	return p
}

func (h *handlers) checkTarget(ctx context.Context, in *checkTargetInput) (*checkTargetOutput, error) {
	host, port, err := net.SplitHostPort(in.Body.Dest)
	if err != nil || host == "" {
		return nil, huma.Error422UnprocessableEntity("bad_dest", &huma.ErrorDetail{Location: "body.dest", Message: "dest_format"})
	}
	// The check dials from the server; internal addresses are not its business, except the
	// panel's own port (self-steal).
	selfSteal := (host == "127.0.0.1" || host == "localhost") && port == strconv.Itoa(h.panelPort(ctx))
	if !selfSteal && !proto.PublicHost(host) {
		return nil, huma.Error422UnprocessableEntity("bad_dest", &huma.ErrorDetail{Location: "body.dest", Message: "reality_dest_private"})
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return &checkTargetOutput{Body: scan.Check(ctx, in.Body.Dest, in.Body.SNI)}, nil
}

type scanTargetsInput struct {
	NodeID int64 `query:"node_id" default:"1" minimum:"1" doc:"Нода, рядом с которой искать"`
}

func (h *handlers) scanTargets(ctx context.Context, in *scanTargetsInput) (*scanTargetsOutput, error) {
	if !scanning.TryLock() {
		return nil, huma.Error409Conflict("scan_busy")
	}
	defer scanning.Unlock()
	node, err := h.nodeOf(ctx, in.NodeID)
	if err != nil {
		return nil, err
	}
	var publicHost, domain string
	if publicHost, err = h.d.Settings.String(ctx, settings.KeyPublicHost); err != nil {
		return nil, err
	}
	if domain, err = h.d.Settings.String(ctx, settings.KeyDomain); err != nil {
		return nil, err
	}
	local := node.Address == ""
	if !local {
		publicHost, domain = node.PublicHost, ""
	}
	ip := publicHost
	if _, err := netip.ParseAddr(ip); err != nil {
		addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", publicHost)
		if err != nil || len(addrs) == 0 {
			return nil, huma.Error422UnprocessableEntity("scan_no_ip")
		}
		ip = addrs[0].String()
	}
	out := &scanTargetsOutput{}
	out.Body.IP = ip
	// Self-steal: the SNI is the server's own domain and matches its IP, the target is the
	// panel with its Let's Encrypt certificate.
	if local && domain != "" && h.d.Cert != nil && h.d.Cert().Kind == "letsencrypt" {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		r := scan.Check(cctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(h.panelPort(ctx))), domain)
		cancel()
		out.Body.SelfSteal = &r
	}
	// The node scans its own network, so RTTs are what REALITY will see. A node older than
	// 0.3 cannot: then the panel scans for it.
	var fromNode bool
	if h.d.Nodes != nil {
		if r, err := h.d.Nodes.ScanTargets(ctx, node.ID, nodeapi.TargetScanRequest{IP: ip, Limit: 12}); err == nil {
			out.Body.Scanned, out.Body.Results, fromNode = r.Scanned, r.Results, true
		}
	}
	if !fromNode {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if out.Body.Results, out.Body.Scanned, err = scan.Neighbors(sctx, ip, 12); err != nil && sctx.Err() == nil {
			return nil, huma.Error422UnprocessableEntity("scan_no_ip")
		}
	}
	if out.Body.Results == nil {
		out.Body.Results = []scan.Result{}
	}
	return out, nil
}
