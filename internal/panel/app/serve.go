package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/acme"
	"mikan/internal/panel/config"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/tlscert"
)

func Serve(ctx context.Context, cfg config.Config, version string, web fs.FS) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := domain.Seed(ctx, st, time.Now()); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	ep, err := settings.New(st.Q).Endpoint(ctx)
	if err != nil {
		return err
	}
	host := ep.Host
	if host == "" {
		host = "localhost"
	}
	// The self-signed certificate is long-lived and pinned in Hysteria2/TUIC links, so
	// renewing the panel's public certificate never recreates the node's QUIC listeners.
	tlsDir := filepath.Join(cfg.DataDir, "tls")
	self, err := tlscert.LoadOrCreateSelfSigned(tlsDir, host, time.Now())
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	holder := &tlscert.Holder{}
	holder.Set(self)

	opts := Options{Version: version, Web: web, TrustProxy: cfg.TrustProxy, Log: logger, Now: time.Now}
	opts.TLS = func() (*nodeapi.TLSFiles, string, error) {
		c, k, pin, err := tlscert.PEM(tlsDir)
		if err != nil {
			return nil, "", err
		}
		return &nodeapi.TLSFiles{CertPEM: c, KeyPEM: k}, pin, nil
	}
	if cfg.NodeSocket != "" {
		opts.Node = nodeapi.NewUnixClient(cfg.NodeSocket)
	}
	var certs *acme.Manager
	if !cfg.Dev {
		certs = acme.New(cfg.DataDir, holder, self, settings.New(st.Q), logger, time.Now)
		opts.Certs = certs
	}
	p, err := NewPanel(st, opts)
	if err != nil {
		return err
	}
	paths, err := p.ApplyPaths(ctx)
	if err != nil {
		return err
	}
	if paths.Admin == "" {
		logger.Warn("panel is not initialized yet: run `mikan admin bootstrap`")
	}
	go p.Run(ctx)
	if certs != nil {
		go certs.Run(ctx)
	}

	httpSrv := &http.Server{
		Handler:           p.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(dropHandshakeNoise{}, "", log.LstdFlags),
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	if !cfg.Dev {
		ln = tls.NewListener(ln, &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: holder.Get, NextProtos: []string{"h2", "http/1.1"}})
	}
	logger.Info("panel started", "listen", cfg.Listen, "tls", !cfg.Dev, "version", version)

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// Internet scanners produce a constant stream of failed TLS handshakes; they are not actionable.
type dropHandshakeNoise struct{}

func (dropHandshakeNoise) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("TLS handshake error")) {
		return len(p), nil
	}
	return os.Stderr.Write(p)
}
