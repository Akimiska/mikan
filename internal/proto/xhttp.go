package proto

import (
	"regexp"
	"strconv"
	"strings"
)

// XHTTPTuning is what an XHTTP inbound may change past its defaults, as the inbound form
// shows it: the server's xhttp-config keys both ends read (they reach the apps, see
// xhttpShared) and the client's own connection reuse, which the node cannot tell them.
// "" or false: the default of the core.
type XHTTPTuning struct {
	Mode string `json:"mode" required:"false" enum:",stream-one,stream-up,packet-up" doc:"Режим XHTTP; пусто — по умолчанию"`
	// Padding hides the sizes of requests; obfs mode moves it out of the usual place.
	PaddingBytes     string `json:"x_padding_bytes" required:"false" doc:"Размер паддинга, байт: число или диапазон «100-1000»"`
	PaddingObfs      bool   `json:"x_padding_obfs_mode" required:"false" doc:"Паддинг в своём месте вместо Referer: нужен свежий Xray или mihomo 1.19.31+ у клиентов"`
	PaddingPlacement string `json:"x_padding_placement" required:"false" enum:",queryInHeader,header,cookie,query"`
	PaddingKey       string `json:"x_padding_key" required:"false"`
	PaddingHeader    string `json:"x_padding_header" required:"false"`
	PaddingMethod    string `json:"x_padding_method" required:"false" enum:",repeat-x,tokenish"`
	UplinkMethod     string `json:"uplink_http_method" required:"false" enum:",POST,PUT,PATCH"`
	UplinkChunkSize  string `json:"uplink_chunk_size" required:"false" doc:"Размер куска отправки, байт: число или диапазон"`
	MaxEachPostBytes string `json:"sc_max_each_post_bytes" required:"false" doc:"Наибольший запрос отправки (packet-up), байт: число или диапазон"`
	// The client's own: how many HTTP/2 connections carry how many streams, and for how long.
	MaxConcurrency     string `json:"xmux_max_concurrency" required:"false" doc:"Потоков на соединение: диапазон «16-32»"`
	MaxConnections     string `json:"xmux_max_connections" required:"false" doc:"Соединений сразу; взаимоисключается с потоками"`
	MaxReuseTimes      string `json:"xmux_c_max_reuse_times" required:"false" doc:"Сколько раз переиспользовать соединение"`
	MaxRequestTimes    string `json:"xmux_h_max_request_times" required:"false" doc:"Запросов на соединение HTTP/2"`
	MaxReusableSecs    string `json:"xmux_h_max_reusable_secs" required:"false" doc:"Сколько секунд соединение живёт"`
	MinPostsIntervalMs string `json:"sc_min_posts_interval_ms" required:"false" doc:"Пауза между запросами отправки (packet-up), мс"`
}

// rangeKind says whether a key holds a byte or count range ("N" or "N-M"), and whether it
// must be above zero.
type rangeKind int

const (
	noRange   rangeKind = iota
	anyRange            // 0 and up
	someRange           // 1 and up
)

// The server keys of XHTTPTuning in xhttp-config; a range key is checked by its kind
// (XHTTPTuning.check), so a key added here is read, written and checked at once.
var xhttpServerKeys = []struct {
	key  string
	get  func(*XHTTPTuning) *string
	kind rangeKind
}{
	{"mode", func(x *XHTTPTuning) *string { return &x.Mode }, noRange},
	{"x-padding-bytes", func(x *XHTTPTuning) *string { return &x.PaddingBytes }, anyRange},
	{"x-padding-placement", func(x *XHTTPTuning) *string { return &x.PaddingPlacement }, noRange},
	{"x-padding-key", func(x *XHTTPTuning) *string { return &x.PaddingKey }, noRange},
	{"x-padding-header", func(x *XHTTPTuning) *string { return &x.PaddingHeader }, noRange},
	{"x-padding-method", func(x *XHTTPTuning) *string { return &x.PaddingMethod }, noRange},
	{"uplink-http-method", func(x *XHTTPTuning) *string { return &x.UplinkMethod }, noRange},
	{"uplink-chunk-size", func(x *XHTTPTuning) *string { return &x.UplinkChunkSize }, someRange},
	{"sc-max-each-post-bytes", func(x *XHTTPTuning) *string { return &x.MaxEachPostBytes }, someRange},
}

// The client keys: mihomo's reuse-settings and the Xray share link's xmux; all ranges.
var xmuxKeys = []struct {
	mihomo, xray string
	get          func(*XHTTPTuning) *string
}{
	{"max-concurrency", "maxConcurrency", func(x *XHTTPTuning) *string { return &x.MaxConcurrency }},
	{"max-connections", "maxConnections", func(x *XHTTPTuning) *string { return &x.MaxConnections }},
	{"c-max-reuse-times", "cMaxReuseTimes", func(x *XHTTPTuning) *string { return &x.MaxReuseTimes }},
	{"h-max-request-times", "hMaxRequestTimes", func(x *XHTTPTuning) *string { return &x.MaxRequestTimes }},
	{"h-max-reusable-secs", "hMaxReusableSecs", func(x *XHTTPTuning) *string { return &x.MaxReusableSecs }},
}

// XHTTPOf reads the tuning of an XHTTP template; ok is false for other transports.
func XHTTPOf(t Template) (x XHTTPTuning, ok bool) {
	sec := t.section("xhttp-config")
	if sec == nil {
		return x, false
	}
	for _, k := range xhttpServerKeys {
		*k.get(&x) = scalar(sec[k.key])
	}
	x.PaddingObfs, _ = sec["x-padding-obfs-mode"].(bool)
	c := xhttpClient(t)
	for _, k := range xmuxKeys {
		if xm, _ := c["xmux"].(map[string]any); xm != nil {
			*k.get(&x) = scalar(xm[k.mihomo])
		}
	}
	x.MinPostsIntervalMs = scalar(c["sc-min-posts-interval-ms"])
	return x, true
}

// scalar reads a YAML value typed as a string or a number ("1000" and 1000 alike).
func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
	}
	return ""
}

// xhttpClient is mikan.client.xhttp of t (nil when unset).
func xhttpClient(t Template) map[string]any {
	ext, _ := t[extKey].(map[string]any)
	cl, _ := ext["client"].(map[string]any)
	x, _ := cl["xhttp"].(map[string]any)
	return x
}

// SetXHTTP writes x over the template's XHTTP settings: every field, so an empty one goes
// back to the core's default.
func SetXHTTP(t Template, x XHTTPTuning) error {
	sec := t.section("xhttp-config")
	if sec == nil {
		return fail("xhttp_no_xhttp", "xhttp-config")
	}
	if err := x.check(); err != nil {
		return err
	}
	for _, k := range xhttpServerKeys {
		if v := strings.TrimSpace(*k.get(&x)); v != "" {
			sec[k.key] = v
		} else {
			delete(sec, k.key)
		}
	}
	if x.PaddingObfs {
		sec["x-padding-obfs-mode"] = true
	} else {
		delete(sec, "x-padding-obfs-mode")
	}
	client := map[string]any{}
	xm := map[string]any{}
	for _, k := range xmuxKeys {
		if v := strings.TrimSpace(*k.get(&x)); v != "" {
			xm[k.mihomo] = v
		}
	}
	if len(xm) > 0 {
		client["xmux"] = xm
	}
	if v := strings.TrimSpace(x.MinPostsIntervalMs); v != "" {
		client["sc-min-posts-interval-ms"] = v
	}
	ext, _ := t[extKey].(map[string]any)
	if ext == nil {
		ext = map[string]any{}
	}
	cl, _ := ext["client"].(map[string]any)
	if cl == nil {
		cl = map[string]any{}
	}
	if len(client) > 0 {
		cl["xhttp"] = client
	} else {
		delete(cl, "xhttp")
	}
	if len(cl) > 0 {
		ext["client"] = cl
	} else {
		delete(ext, "client")
	}
	if len(ext) > 0 {
		t[extKey] = ext
	} else {
		delete(t, extKey)
	}
	return nil
}

var xhttpToken = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// check refuses what the core would refuse when the node loads it, by field.
func (x XHTTPTuning) check() error {
	enum := func(v, field string, ok ...string) error {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		for _, o := range ok {
			if v == o {
				return nil
			}
		}
		return fail("xhttp_value", field)
	}
	if err := enum(x.Mode, "xhttp-config.mode", "stream-one", "stream-up", "packet-up"); err != nil {
		return fail("config_xhttp_mode", "xhttp-config.mode")
	}
	for _, e := range []struct {
		v, field string
		ok       []string
	}{
		{x.PaddingPlacement, "xhttp-config.x-padding-placement", []string{"queryInHeader", "header", "cookie", "query"}},
		{x.PaddingMethod, "xhttp-config.x-padding-method", []string{"repeat-x", "tokenish"}},
		{x.UplinkMethod, "xhttp-config.uplink-http-method", []string{"POST", "PUT", "PATCH"}},
	} {
		if err := enum(e.v, e.field, e.ok...); err != nil {
			return err
		}
	}
	for _, v := range []struct{ v, field string }{{x.PaddingKey, "xhttp-config.x-padding-key"}, {x.PaddingHeader, "xhttp-config.x-padding-header"}} {
		if s := strings.TrimSpace(v.v); s != "" && !xhttpToken.MatchString(s) {
			return fail("xhttp_value", v.field)
		}
	}
	type rangeField struct {
		v, field string
		min1     bool
	}
	var ranges []rangeField
	for _, k := range xhttpServerKeys {
		if k.kind != noRange {
			ranges = append(ranges, rangeField{*k.get(&x), "xhttp-config." + k.key, k.kind == someRange})
		}
	}
	for _, k := range xmuxKeys {
		ranges = append(ranges, rangeField{*k.get(&x), extKey + ".client.xhttp.xmux." + k.mihomo, false})
	}
	ranges = append(ranges, rangeField{x.MinPostsIntervalMs, extKey + ".client.xhttp.sc-min-posts-interval-ms", true})
	for _, r := range ranges {
		if s := strings.TrimSpace(r.v); s != "" && !validRange(s, r.min1) {
			return fail("xhttp_range", r.field)
		}
	}
	// Xray and mihomo take one of the two: streams per connection or connections.
	if strings.TrimSpace(x.MaxConcurrency) != "" && strings.TrimSpace(x.MaxConnections) != "" {
		return fail("xhttp_xmux_both", extKey+".client.xhttp.xmux.max-connections")
	}
	return nil
}

// validRange reads "N" or "N-M" the way the cores do: whole numbers, N ≤ M, above zero
// where the core needs it.
func validRange(s string, min1 bool) bool {
	lo, hi, isRange := strings.Cut(s, "-")
	if !isRange {
		hi = lo
	}
	a, err1 := strconv.Atoi(lo)
	b, err2 := strconv.Atoi(hi)
	if err1 != nil || err2 != nil || a < 0 || a > b || b > 1<<30 {
		return false
	}
	return !min1 || a > 0
}
