package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"mikan/internal/panel/store/db"
)

// Legacy migrations are exclusively for normalizing a COPY during SQLite import.
//
//go:embed migrations/*.sql
var migrations embed.FS

//go:embed postgres/*.sql
var postgresMigrations embed.FS

type Store struct {
	DB      *sql.DB
	Q       *db.Queries
	cleanup func() error

	conflicts atomic.Uint64
}

// Conflicts counts the serialization conflicts Tx has retried since the store opened.
func (s *Store) Conflicts() uint64 { return s.conflicts.Load() }

func Open(ctx context.Context, dataDir string) (*Store, error) {
	return OpenPostgres(ctx, dataDir, os.Getenv("MIKAN_DATABASE_URL"))
}

// OpenPostgres never falls back to SQLite. A legacy installation must first complete
// database migrate while all its writers are stopped; an empty PG is not a new install.
func OpenPostgres(ctx context.Context, dataDir, dsn string) (*Store, error) {
	conn, err := PreparePostgresImport(ctx, dsn)
	if err != nil {
		return nil, err
	}
	_, legacyErr := os.Stat(filepath.Join(dataDir, "mikan.db"))
	_, markerErr := os.Stat(filepath.Join(dataDir, MigrationMarker))
	if legacyErr == nil || markerErr == nil {
		var report string
		if err := conn.QueryRowContext(ctx, "SELECT report FROM mikan_sqlite_import WHERE id = 1").Scan(&report); err != nil {
			conn.Close()
			return nil, errors.New("SQLite data have not been imported: stop the panel and run mikan database migrate")
		}
		// A crash after COMMIT but before writing the marker is recovered from PostgreSQL.
		if err := writeMigrationMarker(dataDir, []byte(report)); err != nil {
			conn.Close()
			return nil, err
		}
	} else if !errors.Is(legacyErr, fs.ErrNotExist) {
		conn.Close()
		return nil, fmt.Errorf("legacy database: %w", legacyErr)
	}
	if markerErr != nil && !errors.Is(markerErr, fs.ErrNotExist) {
		conn.Close()
		return nil, markerErr
	}
	if err := migratePostgres(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	if err := ensureLocalNode(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return &Store{DB: conn, Q: db.New(conn)}, nil
}

func ensureLocalNode(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, "INSERT INTO nodes(id,created_at,updated_at) VALUES(1,$1,$1) ON CONFLICT(id) DO NOTHING", time.Now().Unix())
	return err
}

func connectPostgres(ctx context.Context, dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, errors.New("MIKAN_DATABASE_URL is required; run the current mikan installer to set up PostgreSQL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid MIKAN_DATABASE_URL")
	}
	cfg.ConnectTimeout = 10 * time.Second
	conn := stdlib.OpenDB(*cfg)
	conn.SetMaxOpenConns(16)
	conn.SetMaxIdleConns(4)
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	return conn, nil
}

func migratePostgres(ctx context.Context, conn *sql.DB) error {
	fsys, err := fs.Sub(postgresMigrations, "postgres")
	if err != nil {
		return err
	}
	return finishPostgresImport(ctx, conn, fsys)
}

func postgresProvider(ctx context.Context, conn *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	var database, schema string
	if err := conn.QueryRowContext(ctx, "SELECT current_database(),current_schema()").Scan(&database, &schema); err != nil {
		return nil, err
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "mikan-goose:%s:%s", database, schema)
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(int64(h.Sum64())))
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, conn, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL migrations: %w", err)
	}
	// GetDBVersion initializes Goose's table under the session lock. GetVersions
	// initializes without it and races when two fresh processes start together.
	current, err := p.GetDBVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL migration versions: %w", err)
	}
	sources := p.ListSources()
	target := sources[len(sources)-1].Version
	if current > target {
		return nil, fmt.Errorf("PostgreSQL schema %d is newer than this binary supports (%d); downgrade refused", current, target)
	}
	return p, nil
}

func (s *Store) Close() error {
	err := s.DB.Close()
	if s.cleanup != nil {
		err = errors.Join(err, s.cleanup())
	}
	return err
}

// txAttempts bounds the retries of a serialization conflict; with the backoff below the
// last attempt starts within about three seconds at most.
const txAttempts = 16

// Tx preserves read/check/write invariants (quotas, payments, ports, slot numbers) with
// serializable transactions. Callbacks only change database state: a serialization
// conflict retries the whole callback, never just its final statement.
func (s *Store) Tx(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.retry(ctx, sql.LevelSerializable, fn)
}

// TxRC runs fn in one READ COMMITTED transaction: for writes that hold no read/check/write
// invariant across rows (settings, catalog edits, upserts that add in place). It never
// fails on a serialization conflict; a deadlock still retries the whole callback, so the
// same rule holds: callbacks only change database state.
func (s *Store) TxRC(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.retry(ctx, sql.LevelReadCommitted, fn)
}

func (s *Store) retry(ctx context.Context, level sql.IsolationLevel, fn func(q *db.Queries) error) error {
	for attempt := 0; ; attempt++ {
		err := s.txOnce(ctx, level, fn)
		var pe *pgconn.PgError
		if err == nil || attempt == txAttempts-1 || !errors.As(err, &pe) || (pe.Code != "40001" && pe.Code != "40P01") {
			return err
		}
		// Exponential backoff with full jitter: writers that collided together must not
		// all come back together, or the same conflict repeats until the attempts run out.
		s.conflicts.Add(1)
		window := min(5*time.Millisecond<<attempt, 250*time.Millisecond)
		timer := time.NewTimer(rand.N(window) + time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Store) txOnce(ctx context.Context, level sql.IsolationLevel, fn func(q *db.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: level})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(s.Q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

func IsUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}
