package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"mikan/internal/panel/store/db"
)

// OpenTest gives each fixture its own PostgreSQL schema. Reopening the same directory
// uses the same schema, like the old file-backed fixtures did.
func OpenTest(ctx context.Context, dataDir string) (*Store, error) {
	dsn := os.Getenv("MIKAN_TEST_DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("tests require MIKAN_TEST_DATABASE_URL pointing to a disposable PostgreSQL database")
	}
	admin, err := connectPostgres(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer admin.Close()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, ".test-postgres-schema")
	name, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		var token [12]byte
		if _, err := rand.Read(token[:]); err != nil {
			return nil, err
		}
		name = []byte("mikan_test_" + hex.EncodeToString(token[:]))
		if err := os.WriteFile(path, name, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	schema := pgx.Identifier{string(name)}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.RuntimeParams["search_path"] = string(name)
	conn := stdlib.OpenDB(*cfg)
	conn.SetMaxOpenConns(16)
	conn.SetMaxIdleConns(4)
	if err := migratePostgres(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	if err := ensureLocalNode(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return &Store{DB: conn, Q: db.New(conn), cleanup: func() error {
		c, err := connectPostgres(context.Background(), dsn)
		if err != nil {
			return err
		}
		defer c.Close()
		_, err = c.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		return err
	}}, nil
}
