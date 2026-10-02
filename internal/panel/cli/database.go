package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"mikan/internal/panel/config"
	"mikan/internal/panel/store"
)

func databaseCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("database needs migrate, backup FILE or restore FILE")
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	dsn := os.Getenv("MIKAN_DATABASE_URL")
	if dsn == "" {
		return errors.New("MIKAN_DATABASE_URL is required")
	}
	switch args[0] {
	case "migrate":
		if len(args) != 1 {
			return errors.New("usage: mikan database migrate")
		}
		pg, err := store.PreparePostgresImport(ctx, dsn)
		if err != nil {
			return err
		}
		defer pg.Close()
		before, err := store.SchemaVersion(ctx, pg)
		if err != nil {
			return err
		}
		report, err := store.ImportSQLite(ctx, pg, cfg.DataDir)
		if err != nil {
			return err
		}
		if err := store.FinishPostgresImport(ctx, pg); err != nil {
			return err
		}
		after, err := store.SchemaVersion(ctx, pg)
		if err != nil {
			return err
		}
		// The installer reads this line: an unchanged schema lets a failed update go back.
		fmt.Printf("PostgreSQL schema version: %d -> %d\n", before, after)
		if report == nil {
			fmt.Println("PostgreSQL schema is ready; no legacy SQLite database")
		} else {
			fmt.Printf("SQLite import verified: %d tables; original SQLite preserved\n", len(report.Tables))
		}
		return nil
	case "backup":
		if len(args) != 2 {
			return errors.New("usage: mikan database backup FILE")
		}
		if err := databaseBackup(ctx, dsn, args[1], ""); err != nil {
			return err
		}
		fmt.Println("Database copied to", args[1])
		return nil
	case "restore":
		if len(args) != 2 {
			return errors.New("usage: mikan database restore FILE (panel must be stopped)")
		}
		f, err := os.Open(args[1])
		if err != nil {
			return err
		}
		magic := make([]byte, 16)
		_, readErr := io.ReadFull(f, magic)
		f.Close()
		if readErr != nil {
			return readErr
		}
		if string(magic) == "SQLite format 3\x00" {
			if err := store.RestoreSQLite(ctx, dsn, cfg.DataDir, args[1]); err != nil {
				return err
			}
		} else if string(magic[:5]) == "PGDMP" {
			err := store.RestorePostgres(ctx, dsn, cfg.DataDir, func(ctx context.Context) error {
				return postgresTool(ctx, dsn, "pg_restore", "--dbname=", "--clean", "--if-exists", "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl", args[1])
			})
			if err != nil {
				return err
			}
		} else {
			return errors.New("unsupported database backup format")
		}
		fmt.Println("Database restored; start the panel after restoring its matching files")
		return nil
	default:
		return fmt.Errorf("unknown database command %q", args[0])
	}
}

func databaseBackup(ctx context.Context, dsn, path, schema string) error {
	if schema == "" {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			return errors.New("invalid MIKAN_DATABASE_URL")
		}
		cfg.ConnectTimeout = 10 * time.Second
		conn := stdlib.OpenDB(*cfg)
		qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = conn.QueryRowContext(qctx, "SELECT current_schema()").Scan(&schema)
		cancel()
		conn.Close()
		if err != nil {
			return fmt.Errorf("backup schema: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	args := []string{"--format=custom", "--no-owner", "--no-acl"}
	if schema != "" {
		args = append(args, "--schema="+schema)
	}
	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	cmd.Env, err = postgresEnv(dsn)
	if err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	cmd.Stdout = f
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return fmt.Errorf("PostgreSQL backup failed: %w%s", err, toolOutput(dsn, stderr.String()))
	}
	return nil
}

// toolOutput is the tail of a client's diagnostics with the password masked: libpq
// takes it from PGPASSWORD and does not print it, the mask is for a server that echoes.
func toolOutput(dsn, out string) string {
	out = strings.TrimSpace(out)
	if cfg, err := pgx.ParseConfig(dsn); err == nil && cfg.Password != "" {
		out = strings.ReplaceAll(out, cfg.Password, "***")
	}
	if len(out) > 2000 {
		out = "…" + out[len(out)-2000:]
	}
	if out == "" {
		return ""
	}
	return ": " + out
}
func postgresEnv(dsn string) ([]string, error) {
	// PGDATABASE does not expand a URI into user/password/host. Give libpq each
	// field explicitly, especially with UID 65532 which has no OS login in the image.
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid MIKAN_DATABASE_URL")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, errors.New("database backup/restore requires a postgres:// or postgresql:// MIKAN_DATABASE_URL")
	}
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "PG") {
			env = append(env, value)
		}
	}
	env = append(env, "PGHOST="+cfg.Host, "PGPORT="+strconv.Itoa(int(cfg.Port)), "PGUSER="+cfg.User, "PGPASSWORD="+cfg.Password, "PGDATABASE="+cfg.Database, "PGCONNECT_TIMEOUT=10")
	query := u.Query()
	for key, variable := range map[string]string{"sslmode": "PGSSLMODE", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY", "sslrootcert": "PGSSLROOTCERT", "sslcrl": "PGSSLCRL", "sslcrldir": "PGSSLCRLDIR", "sslpassword": "PGSSLPASSWORD", "sslcertmode": "PGSSLCERTMODE", "gssencmode": "PGGSSENCMODE", "channel_binding": "PGCHANNELBINDING", "target_session_attrs": "PGTARGETSESSIONATTRS", "options": "PGOPTIONS"} {
		if value := query.Get(key); value != "" {
			env = append(env, variable+"="+value)
		}
	}
	return env, nil
}
func postgresTool(ctx context.Context, dsn, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var err error
	cmd.Env, err = postgresEnv(dsn)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w%s", name, err, toolOutput(dsn, stderr.String()))
	}
	return nil
}
