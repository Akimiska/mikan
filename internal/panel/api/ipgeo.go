package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/nodeapi"
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
	huma.Register(h.api, huma.Operation{OperationID: "check-node-regions", Metadata: supportDiagnostic, Method: http.MethodPost, Path: "/api/v1/nodes/{id}/regions", Summary: "Регион ноды в сервисах",
		Description: "Нода сама открывает Google, YouTube, Netflix, ChatGPT и другие сайты по IPv4 и IPv6 и читает, какую страну они ей дают или пускают ли её. Около 10 секунд; на ноде одна проверка за раз (409 regions_busy).", Tags: []string{"node"}}, h.checkNodeRegions)
}

type regionsOutput struct{ Body nodeapi.ServiceRegions }

// checkNodeRegions asks the sites from the node. Its addresses stay the panel's business,
// as with the geo bases: the answer carries the countries only.
func (h *handlers) checkNodeRegions(ctx context.Context, in *nodeIDInput) (*regionsOutput, error) {
	if _, err := h.getNode(ctx, in.ID); err != nil {
		return nil, err
	}
	if h.d.Nodes == nil {
		return nil, huma.Error502BadGateway("node_unavailable")
	}
	res, err := h.d.Nodes.ServiceRegions(ctx, in.ID)
	if err != nil {
		var ne *nodeapi.Error
		var se *nodeapi.StatusError
		switch {
		case errors.As(err, &ne) && ne.Code == "regions_busy":
			return nil, huma.Error409Conflict("regions_busy")
		case errors.As(err, &se) && se.Status == http.StatusNotFound:
			return nil, huma.Error409Conflict("node_too_old")
		}
		h.d.Log.Warn("node service regions", "node", in.ID, "err", err)
		return nil, huma.Error502BadGateway("node_unavailable")
	}
	res.IPv4, res.IPv6 = "", ""
	if res.Services == nil {
		res.Services = []nodeapi.ServiceRegion{}
	}
	return &regionsOutput{Body: res}, nil
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
