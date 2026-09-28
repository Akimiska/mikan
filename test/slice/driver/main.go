// driver runs the vertical slice against a real panel and node:
//
//	prepare: log in, create a user by tariff, turn its subscription into a client config
//	verify:  push traffic through every protocol and check the panel's accounting
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const (
	panelURL  = "https://panel:2053/slice-admin-path-0000"
	subPrefix = "https://panel:2053/slicesub0000/"
	mib       = 1 << 20
)

var protos = []struct{ name, proxy string }{
	{"vision", "VLESS Vision"}, {"xhttp", "VLESS XHTTP"}, {"hy2", "Hysteria2"}, {"tuic", "TUIC"},
}

type panel struct {
	hc   *http.Client
	csrf string
}

func login() *panel {
	jar, _ := cookiejar.New(nil)
	// The panel uses its self-signed certificate inside the test network.
	p := &panel{hc: &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	p.call("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": os.Getenv("SLICE_PW")}, &me)
	p.csrf = me.CSRF
	return p
}

func (p *panel) call(method, path string, in, out any) int {
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, panelURL+path, body)
	req.Header.Set("Content-Type", "application/json")
	if p.csrf != "" {
		req.Header.Set("X-CSRF-Token", p.csrf)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		log.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		log.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			log.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

type user struct {
	ID       int64  `json:"id"`
	State    string `json:"state"`
	UsedUp   int64  `json:"used_up"`
	UsedDown int64  `json:"used_down"`
	SubURL   string `json:"sub_url"`
}

func main() {
	log.SetFlags(log.Ltime)
	switch os.Args[1] {
	case "prepare":
		prepare()
	case "verify":
		verify()
	}
}

func prepare() {
	p := login()
	waitNode(p)
	var tariffs []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	p.call("GET", "/api/v1/tariffs", nil, &tariffs)
	var tariffID int64
	for _, t := range tariffs {
		if t.Name == "Стандарт" {
			tariffID = t.ID
		}
	}
	var u user
	p.call("POST", "/api/v1/users", map[string]any{"name": "Slice User", "tariff_id": tariffID}, &u)
	log.Printf("created user %d, subscription %s", u.ID, u.SubURL)
	if !strings.HasPrefix(u.SubURL, "https://node:2053/slicesub0000/") {
		log.Fatalf("unexpected sub_url %q", u.SubURL)
	}
	token := u.SubURL[strings.LastIndex(u.SubURL, "/")+1:]

	// URI format for Happ-like clients: four links.
	links := fetchSub(token, "Happ/3.4.1")
	decoded, err := base64.StdEncoding.DecodeString(string(links))
	if err != nil || strings.Count(string(decoded), "\n") != 3 {
		log.Fatalf("uri subscription: %v %q", err, decoded)
	}

	// mihomo profile: expose one mixed port per proxy so the verify phase can pick a protocol.
	var cfg map[string]any
	if err := json.Unmarshal(fetchSub(token, "mihomo/1.19.31"), &cfg); err != nil {
		log.Fatalf("clash subscription is not JSON/YAML: %v", err)
	}
	cfg["allow-lan"] = true
	cfg["bind-address"] = "*"
	delete(cfg, "dns")
	var listeners []map[string]any
	for i, pr := range protos {
		listeners = append(listeners, map[string]any{"name": "in-" + pr.name, "type": "mixed", "listen": "0.0.0.0", "port": 11001 + i, "proxy": pr.proxy})
	}
	cfg["listeners"] = listeners
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile("/work/client.yaml", raw, 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("/work/user", []byte(strconv.FormatInt(u.ID, 10)), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Print("client config written from the subscription")
}

func waitNode(p *panel) {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var n struct {
			OK        bool `json:"ok"`
			Listeners []struct {
				Name string `json:"name"`
				OK   bool   `json:"ok"`
			} `json:"listeners"`
		}
		p.call("GET", "/api/v1/node", nil, &n)
		ok := n.OK && len(n.Listeners) == 4
		for _, l := range n.Listeners {
			ok = ok && l.OK
		}
		if ok {
			log.Print("node is up with 4 listeners")
			return
		}
		time.Sleep(time.Second)
	}
	log.Fatal("node did not come up with healthy listeners")
}

func fetchSub(token, ua string) []byte {
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	req, _ := http.NewRequest("GET", subPrefix+token, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := hc.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Subscription-Userinfo") == "" {
		log.Fatalf("subscription %s: %d, userinfo %q", ua, resp.StatusCode, resp.Header.Get("Subscription-Userinfo"))
	}
	raw, _ := io.ReadAll(resp.Body)
	return raw
}

func download(port int, n int64) (int64, error) {
	d, _ := proxy.SOCKS5("tcp", net.JoinHostPort("client", strconv.Itoa(port)), nil, &net.Dialer{Timeout: 10 * time.Second})
	c, err := d.Dial("tcp", "target:9000")
	if err != nil {
		return 0, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	hdr := make([]byte, 9)
	hdr[0] = 'D'
	binary.BigEndian.PutUint64(hdr[1:], uint64(n))
	if _, err := c.Write(hdr); err != nil {
		return 0, err
	}
	got, err := io.Copy(io.Discard, c)
	if err == nil && got != n {
		err = fmt.Errorf("short download %d/%d", got, n)
	}
	return got, err
}

func verify() {
	p := login()
	raw, _ := os.ReadFile("/work/user")
	id, _ := strconv.ParseInt(string(raw), 10, 64)
	var before user
	p.call("GET", "/api/v1/users/"+strconv.FormatInt(id, 10), nil, &before)

	const each = 16 * mib
	for i, pr := range protos {
		if _, err := download(11001+i, each); err != nil {
			log.Fatalf("%s: download through the subscription config failed: %v", pr.name, err)
		}
		log.Printf("%s: 16 MiB downloaded", pr.name)
	}
	want := int64(len(protos) * each)
	var after user
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		p.call("GET", "/api/v1/users/"+strconv.FormatInt(id, 10), nil, &after)
		if after.UsedDown-before.UsedDown >= want {
			break
		}
		time.Sleep(time.Second)
	}
	got := after.UsedDown - before.UsedDown
	log.Printf("panel accounted download %d, expected %d", got, want)
	if diff := float64(got-want) / float64(want); diff < -0.01 || diff > 0.01 {
		log.Fatalf("AC-3 failed: panel counted %d, expected %d ±1%%", got, want)
	}

	p.call("PATCH", "/api/v1/users/"+strconv.FormatInt(id, 10), map[string]any{"disabled": true}, nil)
	time.Sleep(1500 * time.Millisecond)
	if _, err := download(11001, mib); err == nil {
		log.Fatal("AC-5 failed: disabled user can still download")
	}
	log.Print("disabled user is cut off")
	p.call("PATCH", "/api/v1/users/"+strconv.FormatInt(id, 10), map[string]any{"disabled": false}, nil)
	time.Sleep(1500 * time.Millisecond)
	if _, err := download(11003, mib); err != nil {
		log.Fatalf("re-enabled user cannot download: %v", err)
	}
	log.Print("re-enabled user works again")
	log.Print("SLICE OK")
}
