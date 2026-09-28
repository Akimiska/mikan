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

func (h *handlers) scanTargets(ctx context.Context, _ *struct{}) (*scanTargetsOutput, error) {
	if !scanning.TryLock() {
		return nil, huma.Error409Conflict("scan_busy")
	}
	defer scanning.Unlock()
	var publicHost, domain string
	var err error
	if publicHost, err = h.d.Settings.String(ctx, settings.KeyPublicHost); err != nil {
		return nil, err
	}
	if domain, err = h.d.Settings.String(ctx, settings.KeyDomain); err != nil {
		return nil, err
	}
	ip := publicHost
	if _, err := netip.ParseAddr(ip); err != nil {
		addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", publicHost)
		if err != nil || len(addrs) == 0 {
			return nil, huma.Error422UnprocessableEntity("scan_no_ip")
		}
		ip = addrs[0].String()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := &scanTargetsOutput{}
	out.Body.IP = ip
	// Self-steal: the SNI is the server's own domain and matches its IP, the target is the
	// panel with its Let's Encrypt certificate.
	if domain != "" && h.d.Cert != nil && h.d.Cert().Kind == "letsencrypt" {
		r := scan.Check(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(h.panelPort(ctx))), domain)
		out.Body.SelfSteal = &r
	}
	results, scanned, err := scan.Neighbors(ctx, ip, 12)
	if err != nil && ctx.Err() == nil {
		return nil, huma.Error422UnprocessableEntity("scan_no_ip")
	}
	out.Body.Scanned = scanned
	out.Body.Results = results
	if out.Body.Results == nil {
		out.Body.Results = []scan.Result{}
	}
	return out, nil
}
