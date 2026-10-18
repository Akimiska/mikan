package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/ipgeo"
	"mikan/internal/panel/reach"
)

type geoOutput struct{ Body ipgeo.GeoCheck }

type geoInput struct {
	Body struct {
		Host string `json:"host" maxLength:"253" doc:"IP или домен сервера"`
	}
}

func (h *handlers) registerGeo() {
	// Like the checks from Russia: nothing of subscribers goes out, the server's address does.
	huma.Register(h.api, huma.Operation{OperationID: "check-node-geo", Metadata: supportDiagnostic, Method: http.MethodPost, Path: "/api/v1/nodes/{id}/geo", Summary: "Страна IP ноды по геобазам",
		Description: "Адрес ноды уходит в публичные геобазы (MaxMind, ipinfo.io, IP2Location и другие); каждая отвечает, в какой стране он для неё.", Tags: []string{"node"}}, h.checkNodeGeo)
	huma.Register(h.api, huma.Operation{OperationID: "check-geo", Metadata: supportDiagnostic, Method: http.MethodPost, Path: "/api/v1/system/geo", Summary: "Страна IP любого сервера по геобазам",
		Description: "Адрес сервера уходит в публичные геобазы.", Tags: []string{"settings"}}, h.checkGeo)
}

// checkNodeGeo asks the geo bases where the node's address is. Its address stays the
// panel's business: a support operator sees the countries only.
func (h *handlers) checkNodeGeo(ctx context.Context, in *nodeIDInput) (*geoOutput, error) {
	n, err := h.getNode(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if h.d.Geo == nil {
		return nil, huma.Error409Conflict("geo_unavailable")
	}
	host := n.PublicHost
	if n.Address == "" {
		ep, err := h.d.Settings.Endpoint(ctx)
		if err != nil {
			return nil, err
		}
		host = ep.Host
	}
	res, err := h.geo(ctx, host)
	if err != nil {
		return nil, err
	}
	res.IP = ""
	return &geoOutput{Body: res}, nil
}

// checkGeo asks the geo bases about any server: one to buy is checked before anything is
// installed on it.
func (h *handlers) checkGeo(ctx context.Context, in *geoInput) (*geoOutput, error) {
	if h.d.Geo == nil {
		return nil, huma.Error409Conflict("geo_unavailable")
	}
	res, err := h.geo(ctx, in.Body.Host)
	if err != nil {
		return nil, err
	}
	return &geoOutput{Body: res}, nil
}

// geo looks a host up (a domain by its first public address, IPv4 first) and asks the bases.
func (h *handlers) geo(ctx context.Context, host string) (ipgeo.GeoCheck, error) {
	host, ok := reach.CanonHost(host)
	if !ok {
		return ipgeo.GeoCheck{}, fieldErr("body.host", "geo_host")
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		if addr, err = publicAddr(ctx, host); err != nil {
			return ipgeo.GeoCheck{}, fieldErr("body.host", "geo_dns")
		}
	}
	res, err := h.d.Geo.Lookup(ctx, addr)
	switch {
	case errors.Is(err, ipgeo.ErrHost):
		return res, fieldErr("body.host", "geo_host")
	case err != nil:
		return res, err
	}
	return res, nil
}

func publicAddr(ctx context.Context, host string) (netip.Addr, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", strings.TrimSuffix(host, "."))
	if err != nil {
		return netip.Addr{}, err
	}
	var v6 netip.Addr
	for _, a := range addrs {
		switch a = a.Unmap(); {
		case !ipgeo.Public(a):
		case a.Is4():
			return a, nil
		case !v6.IsValid():
			v6 = a
		}
	}
	if v6.IsValid() {
		return v6, nil
	}
	return netip.Addr{}, ipgeo.ErrHost
}
