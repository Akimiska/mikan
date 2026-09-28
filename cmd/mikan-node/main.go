// mikan-node runs the VPN data plane: mihomo embedded as a library, controlled by the
// panel over a unix socket. It links mihomo and is therefore distributed under GPL-3.0.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"mikan/internal/node"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mikan-node:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	dataDir := envOr("MIKAN_DATA_DIR", "/data")
	sock := envOr("MIKAN_NODE_SOCKET", "/run/mikan/node.sock")

	release, err := time.ParseDuration(envOr("MIKAN_DEVICE_RELEASE", "60s"))
	if err != nil {
		return fmt.Errorf("MIKAN_DEVICE_RELEASE: %w", err)
	}
	allowPrivate, err := strconv.ParseBool(envOr("MIKAN_ALLOW_PRIVATE", "false"))
	if err != nil {
		return fmt.Errorf("MIKAN_ALLOW_PRIVATE: %w", err)
	}
	eng, err := node.Start(node.Options{DataDir: dataDir, Version: version, Log: log, DeviceRelease: release, AllowPrivate: allowPrivate})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return err
	}
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}
	srv := &http.Server{Handler: node.Handler(eng, log), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("node api", "err", err)
			stop()
		}
	}()
	log.Info("node started", "version", version, "socket", sock)

	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := eng.PersistCounters(); err != nil {
				log.Error("persist counters", "err", err)
			}
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
			return eng.PersistCounters()
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
