// gen writes the e2e fixtures: node desired state and mihomo client configs with
// freshly generated keys, so no key material lives in the repository.
package main

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"mikan/internal/nodeapi"
)

type slot struct{ name, uuid, secret string }

// RealitySettings are the REALITY keys and target of the fixture's two VLESS inbounds.
type RealitySettings struct {
	PrivateKey  string
	PublicKey   string
	ShortIDs    []string
	Dest        string
	ServerNames []string
}

// reality is the template's reality-config of r.
func (r RealitySettings) reality() map[string]any {
	return map[string]any{"dest": r.Dest, "private-key": r.PrivateKey, "short-id": r.ShortIDs, "server-names": r.ServerNames}
}

func main() {
	out := flag.String("out", "/work", "output directory")
	flag.Parse()

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	b64 := base64.RawURLEncoding
	reality := RealitySettings{
		PrivateKey: b64.EncodeToString(priv.Bytes()), PublicKey: b64.EncodeToString(priv.PublicKey().Bytes()),
		ShortIDs: []string{"a1b2c3d4"}, Dest: "www.microsoft.com:443", ServerNames: []string{"www.microsoft.com"},
	}
	slots := make([]slot, 6)
	for i := range slots {
		slots[i] = slot{fmt.Sprintf("s%06d", i+1), uuid(), token()}
	}
	certPEM, keyPEM := selfSigned()
	// The templates the node takes (proto.Template as JSON), as the panel sends them.
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	obfs := token()
	st := nodeapi.DesiredState{
		Revision: 1,
		Inbounds: []nodeapi.Inbound{
			{Name: "vision", Port: "8443", Config: raw(map[string]any{"type": "vless", "reality-config": reality.reality(), "mikan": map[string]any{"flow": "xtls-rprx-vision"}})},
			// "stream-one": "auto" hangs on mihomo v1.19.31 (S-01a).
			{Name: "xhttp", Port: "8444", Config: raw(map[string]any{"type": "vless", "reality-config": reality.reality(), "xhttp-config": map[string]any{"path": "/x7kq", "mode": "stream-one"}})},
			{Name: "hy2", Port: "8445", Config: raw(map[string]any{"type": "hysteria2", "alpn": []string{"h3"}, "obfs": "salamander", "obfs-password": obfs})},
			{Name: "tuic", Port: "8446", Config: raw(map[string]any{"type": "tuic", "alpn": []string{"h3"}, "congestion-controller": "bbr"})},
		},
		TLS: &nodeapi.TLSFiles{CertPEM: certPEM, KeyPEM: keyPEM},
	}
	for _, s := range slots {
		st.Slots = append(st.Slots, nodeapi.Slot{Name: s.name, UUID: s.uuid, Secret: s.secret})
	}

	must(os.MkdirAll(filepath.Join(*out, "clients"), 0o755))
	must(writeJSON(filepath.Join(*out, "state.json"), st))
	// Port plan in client configs: 1<slot><proto>, proto 1=vision 2=xhttp 3=hy2 4=tuic.
	must(os.WriteFile(filepath.Join(*out, "clients", "client.yaml"), []byte(clientConfig(slots, reality, obfs)), 0o644))
}

func clientConfig(slots []slot, r RealitySettings, obfs string) string {
	s := "mixed-port: 0\nallow-lan: true\nbind-address: \"*\"\nmode: rule\nlog-level: warning\nproxies:\n"
	for i, sl := range slots {
		n := i + 1
		s += fmt.Sprintf(`  - {name: vision-%[1]d, type: vless, server: node, port: 8443, uuid: %[2]s, network: tcp, tls: true, udp: true, flow: xtls-rprx-vision, servername: www.microsoft.com, client-fingerprint: chrome, reality-opts: {public-key: %[3]s, short-id: %[4]s}}
  - {name: xhttp-%[1]d, type: vless, server: node, port: 8444, uuid: %[2]s, network: xhttp, tls: true, udp: false, servername: www.microsoft.com, client-fingerprint: chrome, reality-opts: {public-key: %[3]s, short-id: %[4]s}, xhttp-opts: {path: /x7kq, mode: stream-one}}
  - {name: hy2-%[1]d, type: hysteria2, server: node, port: 8445, password: %[5]s, sni: node, skip-cert-verify: true, obfs: salamander, obfs-password: %[6]s}
  - {name: tuic-%[1]d, type: tuic, server: node, port: 8446, uuid: %[2]s, password: %[5]s, alpn: [h3], sni: node, skip-cert-verify: true, congestion-controller: bbr, udp-relay-mode: native}
`, n, sl.uuid, r.PublicKey, r.ShortIDs[0], sl.secret, obfs)
	}
	s += "listeners:\n"
	for i := range slots {
		n := i + 1
		for p, name := range []string{"vision", "xhttp", "hy2", "tuic"} {
			s += fmt.Sprintf("  - {name: in-%s-%d, type: mixed, port: %d, listen: 0.0.0.0, proxy: %s-%d}\n", name, n, 10000+n*10+p+1, name, n)
		}
	}
	s += "tunnels:\n"
	for i := range slots {
		n := i + 1
		for p, name := range []string{"hy2", "tuic"} {
			s += fmt.Sprintf("  - {network: [udp], address: 0.0.0.0:%d, target: target:9001, proxy: %s-%d}\n", 20000+n*10+p+3, name, n)
		}
	}
	s += "rules:\n  - MATCH,DIRECT\n"
	return s
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func token() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func selfSigned() (string, string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "node"}, DNSNames: []string{"node"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	must(err)
	kd, err := x509.MarshalECPrivateKey(key)
	must(err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}))
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
