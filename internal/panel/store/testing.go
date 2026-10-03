package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
)

// OpenTest gives each fixture its own PostgreSQL schema. Reopening the same directory
// uses the same schema, like the old file-backed fixtures did.
func OpenTest(ctx context.Context, dataDir string) (*Store, error) {
	dsn := os.Getenv("MIKAN_TEST_DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("tests require MIKAN_TEST_DATABASE_URL pointing to a disposable PostgreSQL database")
	}
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
	exec := func(ctx context.Context, sql string) error {
		return withConn(ctx, dsn, func(c *pgx.Conn) error { _, err := c.Exec(ctx, sql); return err })
	}
	if err := exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quote(string(name))); err != nil {
		return nil, err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("search_path", string(name))
	u.RawQuery = query.Encode()
	return OpenMigrated(ctx, u.String(), func() error {
		return exec(context.Background(), "DROP SCHEMA IF EXISTS "+quote(string(name))+" CASCADE")
	})
}
