// Package acme obtains and renews the panel's HTTPS certificate from Let's Encrypt:
// a normal certificate for a domain, or a short-lived one (profile "shortlived") for a
// bare IP address. Subscription URLs are fetched by client apps that reject
// self-signed certificates, so an IP-only install still needs a public certificate.
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/tlscert"
)

const letsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"

type Status struct {
	Kind       string    `json:"kind" enum:"self-signed,letsencrypt"`
	Identifier string    `json:"identifier"`
	NotAfter   time.Time `json:"not_after"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

type Manager struct {
	dir       string
	directory string
	holder    *tlscert.Holder
	fallback  *tls.Certificate
	set       *settings.Settings
	log       *slog.Logger
	now       func() time.Time
	mu        sync.Mutex
	status    atomic.Pointer[Status]
	wake      chan struct{}
	challenge string // listen address for http-01, ":80"
}

func New(dataDir string, holder *tlscert.Holder, fallback *tls.Certificate, set *settings.Settings, log *slog.Logger, now func() time.Time) *Manager {
	dir := os.Getenv("MIKAN_ACME_DIRECTORY")
	if dir == "" {
		dir = letsEncrypt
	}
	m := &Manager{dir: filepath.Join(dataDir, "tls", "acme"), directory: dir, holder: holder, fallback: fallback,
		set: set, log: log, now: now, wake: make(chan struct{}, 1), challenge: ":80"}
	m.status.Store(&Status{Kind: "self-signed", CheckedAt: now()})
	return m
}

func (m *Manager) Status() Status { return *m.status.Load() }

// Renew asks the background loop to try again now (e.g. after the admin freed port 80).
func (m *Manager) Renew() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	m.ensure(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.wake:
		}
		m.ensure(ctx)
	}
}

func (m *Manager) identifier(ctx context.Context) (string, error) {
	d, err := m.set.String(ctx, settings.KeyDomain)
	if err != nil || d != "" {
		return d, err
	}
	return m.set.String(ctx, settings.KeyPublicHost)
}

func (m *Manager) ensure(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := m.identifier(ctx)
	st := &Status{Kind: "self-signed", Identifier: id, CheckedAt: m.now()}
	defer func() { m.status.Store(st) }()
	if err != nil {
		st.Error = err.Error()
		return
	}
	if id == "" || id == "localhost" || isPrivate(id) {
		m.useFallback()
		st.Error = "нужен публичный IP или домен"
		return
	}
	if cert, err := m.load(); err == nil && covers(cert.Leaf, id) {
		if !m.needsRenewal(cert.Leaf) {
			m.holder.Set(cert)
			st.Kind, st.NotAfter = "letsencrypt", cert.Leaf.NotAfter
			return
		}
		// Keep serving the current certificate while renewing.
		m.holder.Set(cert)
		st.Kind, st.NotAfter = "letsencrypt", cert.Leaf.NotAfter
	}
	cert, err := m.obtain(ctx, id)
	if err != nil {
		m.log.Warn("acme: certificate not obtained", "identifier", id, "err", err)
		st.Error = humanError(err)
		if st.Kind == "self-signed" {
			m.useFallback()
		}
		return
	}
	m.holder.Set(cert)
	st.Kind, st.NotAfter, st.Error = "letsencrypt", cert.Leaf.NotAfter, ""
	m.log.Info("acme: certificate installed", "identifier", id, "not_after", cert.Leaf.NotAfter)
}

func (m *Manager) useFallback() {
	if m.fallback != nil {
		m.holder.Set(m.fallback)
	}
}

// needsRenewal renews once a third of the lifetime is left: ~2 days for 6-day IP
// certificates, ~30 days for 90-day ones, leaving room for several retries.
func (m *Manager) needsRenewal(leaf *x509.Certificate) bool {
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return leaf.NotAfter.Sub(m.now()) < life/3
}

type user struct {
	email string
	reg   *registration.Resource
	key   crypto.PrivateKey
}

func (u *user) GetEmail() string                        { return u.email }
func (u *user) GetRegistration() *registration.Resource { return u.reg }
func (u *user) GetPrivateKey() crypto.PrivateKey        { return u.key }

func (m *Manager) obtain(ctx context.Context, id string) (*tls.Certificate, error) {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return nil, err
	}
	key, err := m.accountKey()
	if err != nil {
		return nil, err
	}
	email, _ := m.set.String(ctx, settings.KeyACMEEmail)
	u := &user{email: email, key: key}
	cfg := lego.NewConfig(u)
	cfg.CADirURL = m.directory
	cfg.Certificate.KeyType = certcrypto.EC256
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	// Port 80 is taken only for the few seconds of the challenge.
	host, port, _ := net.SplitHostPort(m.challenge)
	if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer(host, port)); err != nil {
		return nil, err
	}
	if u.reg, err = client.Registration.ResolveAccountByKey(); err != nil {
		if u.reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true}); err != nil {
			return nil, fmt.Errorf("register: %w", err)
		}
	}
	req := certificate.ObtainRequest{Domains: []string{id}, Bundle: true}
	if net.ParseIP(id) != nil {
		req.Profile = "shortlived" // Let's Encrypt issues IP certificates only with this profile
	}
	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(m.dir, "cert.pem"), res.Certificate); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(m.dir, "key.pem"), res.PrivateKey); err != nil {
		return nil, err
	}
	return m.load()
}

func (m *Manager) load() (*tls.Certificate, error) {
	c, err := tls.LoadX509KeyPair(filepath.Join(m.dir, "cert.pem"), filepath.Join(m.dir, "key.pem"))
	if err != nil {
		return nil, err
	}
	if c.Leaf == nil {
		return nil, errors.New("no leaf certificate")
	}
	return &c, nil
}

func (m *Manager) accountKey() (crypto.PrivateKey, error) {
	path := filepath.Join(m.dir, "account.key")
	if raw, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, errors.New("account.key: no PEM block")
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	return k, writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func covers(leaf *x509.Certificate, id string) bool {
	if ip := net.ParseIP(id); ip != nil {
		return slices.ContainsFunc(leaf.IPAddresses, ip.Equal)
	}
	return slices.Contains(leaf.DNSNames, id)
}

// isPrivate reports identifiers Let's Encrypt can never validate: private and loopback
// IPs, and single-label names such as docker service names in test setups.
func isPrivate(id string) bool {
	ip := net.ParseIP(id)
	if ip == nil {
		return !strings.Contains(id, ".")
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func humanError(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "address already in use"):
		return "порт 80 занят другой программой — Let's Encrypt проверяет сервер через него"
	case strings.Contains(s, "rateLimited") || strings.Contains(s, "too many"):
		return "Let's Encrypt временно ограничил выпуск (много переустановок) — попробуем позже"
	case strings.Contains(s, "connection") || strings.Contains(s, "timeout"):
		return "Let's Encrypt не достучался до сервера по порту 80 — проверьте файрвол"
	}
	return s
}
