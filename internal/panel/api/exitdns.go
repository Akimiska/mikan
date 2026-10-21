package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/exitdns"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// The DNS at the exit (internal/panel/exitdns): the nodes name the sites users reach
// themselves, so a site sees the node's country whatever DNS the user's device asked.

type ExitDNSView struct {
	Sniff    bool     `json:"sniff" doc:"Нода сама узнаёт, на какой сайт идёт пользователь (TLS, HTTP, QUIC на портах 443, 8443, 80, 8080–8880), и подключается к нему из своей страны: сайты видят страну ноды, а не пользователя"`
	Resolver string   `json:"resolver" enum:"host,cloudflare,google,quad9,custom" doc:"DNS-сервер ноды: host — DNS самого сервера, cloudflare, google, quad9 — их DoH, custom — свой список"`
	Servers  []string `json:"servers" maxItems:"8" doc:"Свои серверы для custom: 1.1.1.1, tls://dns.google, https://1.1.1.1/dns-query, quic://dns.adguard-dns.com; хранятся и при другом выборе"`
}

type ExitDNSInfo struct {
	ExitDNSView
	Presets map[string][]string `json:"presets" doc:"Серверы готовых вариантов"`
}

type exitDNSOutput struct{ Body ExitDNSInfo }

type putExitDNSInput struct{ Body ExitDNSView }

func (h *handlers) registerExitDNS() {
	tags := []string{"nodes"}
	huma.Register(h.api, huma.Operation{OperationID: "get-exit-dns", Method: http.MethodGet, Path: "/api/v1/exit-dns", Summary: "DNS на выходе", Tags: tags}, h.getExitDNS)
	huma.Register(h.api, huma.Operation{OperationID: "update-exit-dns", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPut, Path: "/api/v1/exit-dns", Summary: "Настроить DNS на выходе", Description: "Ноды получают настройку со своим состоянием; ноды старше неё её не применяют.", Tags: tags}, h.updateExitDNS)
}

func exitDNSInfo(c exitdns.Config) ExitDNSInfo {
	return ExitDNSInfo{ExitDNSView: ExitDNSView{Sniff: c.Sniff, Resolver: c.Resolver, Servers: c.Servers}, Presets: exitdns.Presets}
}

func (h *handlers) getExitDNS(ctx context.Context, _ *struct{}) (*exitDNSOutput, error) {
	c, err := exitdns.Load(ctx, h.d.Settings)
	if err != nil {
		return nil, err
	}
	return &exitDNSOutput{Body: exitDNSInfo(c)}, nil
}

func (h *handlers) updateExitDNS(ctx context.Context, in *putExitDNSInput) (*exitDNSOutput, error) {
	c := exitdns.Config{Sniff: in.Body.Sniff, Resolver: in.Body.Resolver, Servers: in.Body.Servers}
	if c.Servers == nil {
		c.Servers = []string{}
	}
	if err := c.Validate(); err != nil {
		var p *exitdns.Problem
		if errors.As(err, &p) {
			return nil, fieldErrValue("body.servers["+strconv.Itoa(p.Index)+"]", "exit_dns_server", p.Value)
		}
		return nil, fieldErr("body.servers", "exit_dns_servers")
	}
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		return settings.Set(ctx, settings.New(q), exitdns.KeyConfig, c)
	})
	if err != nil {
		return nil, err
	}
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "exit_dns.update", "settings", exitdns.KeyConfig,
		map[string]any{"sniff": c.Sniff, "resolver": c.Resolver, "servers": len(c.Servers)})
	return &exitDNSOutput{Body: exitDNSInfo(c)}, nil
}
