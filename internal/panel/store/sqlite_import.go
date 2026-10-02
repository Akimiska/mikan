package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

const MigrationMarker = "postgres-migration.json"

type TableProof struct {
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}
type ImportReport struct {
	Format       int                   `json:"format"`
	ImportedAt   int64                 `json:"imported_at"`
	SourceSHA256 string                `json:"source_sha256"`
	SourceKind   string                `json:"source_kind,omitempty"`
	Tables       map[string]TableProof `json:"tables"`
}

func openSQLite(ctx context.Context, path string) (*sql.DB, error) {
	return sqliteConnection(ctx, path, false)
}
func sqliteConnection(ctx context.Context, path string, readOnly bool) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	v := url.Values{}
	v.Add("_pragma", "foreign_keys(ON)")
	v.Add("_pragma", "busy_timeout(5000)")
	if readOnly {
		v.Set("mode", "ro")
	}
	u.RawQuery = v.Encode()
	c, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	c.SetMaxOpenConns(1)
	if err := c.PingContext(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
func migrateSQLite(ctx context.Context, c *sql.DB) error {
	f, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, c, f)
	if err != nil {
		return err
	}
	var known int64
	for _, source := range p.ListSources() {
		if source.Version > known {
			known = source.Version
		}
	}
	version, err := p.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	if version > known {
		return errors.New("SQLite schema is newer than this importer; use a compatible release")
	}
	_, err = p.Up(ctx)
	return err
}

func writeMigrationMarker(dir string, report []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".postgres-migration-*")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(report); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, MigrationMarker))
}

// ImportSQLite never changes the original file. All copying, constraints, checksums,
// sequence resets and the import record commit together while the panel is stopped.
func ImportSQLite(ctx context.Context, pg *sql.DB, dir string) (*ImportReport, error) {
	return importSQLite(ctx, pg, dir, filepath.Join(dir, "mikan.db"), false)
}

// ImportSQLiteRestore deliberately replaces application data from an explicitly
// selected backup; a failed restore rolls back to the previous PostgreSQL data.
func ImportSQLiteRestore(ctx context.Context, pg *sql.DB, dir, source string) (*ImportReport, error) {
	return importSQLite(ctx, pg, dir, source, true)
}

type importTable struct {
	name          string
	columns, keys []string
	identities    []string
	textKeys      map[string]bool
}

func importSQLite(ctx context.Context, pg *sql.DB, dir, source string, replace bool) (*ImportReport, error) {
	tx, err := pg.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Also serializes concurrent migration/restore invocations in this schema.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(current_schema()), 1296649038)"); err != nil {
		return nil, err
	}
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT report FROM mikan_sqlite_import WHERE id=1").Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if previous != "" && !replace {
		var report ImportReport
		if err := json.Unmarshal([]byte(previous), &report); err != nil {
			return nil, errors.New("invalid committed migration record")
		}
		if err := writeMigrationMarker(dir, []byte(previous)); err != nil {
			return nil, err
		}
		return &report, nil
	}
	if _, err := os.Stat(filepath.Join(dir, MigrationMarker)); err == nil && !replace {
		return nil, errors.New("migration marker exists but PostgreSQL import record is missing; restore the PostgreSQL backup before starting the panel")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(source); errors.Is(err, fs.ErrNotExist) && !replace {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil // Fresh installation; only PostgreSQL schema migrations are needed.
	} else if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(dir, ".sqlite-import-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	snapshot := filepath.Join(tmp, "snapshot.db")
	original, err := sqliteConnection(ctx, source, true)
	if err != nil {
		return nil, fmt.Errorf("read legacy SQLite: %w", err)
	}
	// VACUUM INTO is a consistent snapshot, including committed WAL pages. Opening
	// the source read-only prevents accidental migrations or checkpoints in it.
	_, err = original.ExecContext(ctx, "VACUUM INTO ?", snapshot)
	original.Close()
	if err != nil {
		return nil, fmt.Errorf("snapshot SQLite: %w", err)
	}
	if err := os.Chmod(snapshot, 0600); err != nil {
		return nil, err
	}
	snapshotFile, err := os.Open(snapshot)
	if err != nil {
		return nil, err
	}
	sourceHash := sha256.New()
	_, hashErr := io.Copy(sourceHash, snapshotFile)
	snapshotFile.Close()
	if hashErr != nil {
		return nil, hashErr
	}
	sqlite, err := openSQLite(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	defer sqlite.Close()
	if err := migrateSQLite(ctx, sqlite); err != nil {
		return nil, fmt.Errorf("normalize SQLite snapshot: %w", err)
	}
	var integrity string
	if err := sqlite.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, errors.New("SQLite integrity check failed")
	}
	foreign, err := sqlite.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, err
	}
	invalid := foreign.Next()
	foreignErr := foreign.Err()
	foreign.Close()
	if invalid || foreignErr != nil {
		return nil, errors.New("SQLite foreign key check failed")
	}

	tables, err := importSchema(ctx, sqlite, tx)
	if err != nil {
		return nil, err
	}
	quoted := make([]string, len(tables))
	for i, t := range tables {
		quoted[i] = pgx.Identifier{t.name}.Sanitize()
	}
	if _, err := tx.ExecContext(ctx, "LOCK TABLE "+strings.Join(quoted, ",")+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return nil, err
	}
	if replace {
		if _, err := tx.ExecContext(ctx, "TRUNCATE "+strings.Join(quoted, ",")+" RESTART IDENTITY CASCADE"); err != nil {
			return nil, err
		}
	} else {
		for _, t := range tables {
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM "+pgx.Identifier{t.name}.Sanitize()+")").Scan(&exists); err != nil {
				return nil, err
			}
			if exists {
				return nil, fmt.Errorf("PostgreSQL table %s is occupied; automatic import refused", t.name)
			}
		}
	}
	report := &ImportReport{Format: 1, ImportedAt: time.Now().Unix(), SourceKind: "sqlite", SourceSHA256: hex.EncodeToString(sourceHash.Sum(nil)), Tables: make(map[string]TableProof)}
	for _, t := range tables {
		proof, err := copyTable(ctx, sqlite, tx, t)
		if err != nil {
			return nil, fmt.Errorf("import table %s: %w", t.name, err)
		}
		report.Tables[t.name] = proof
	}
	for _, t := range tables {
		actual, err := tableProof(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		if actual != report.Tables[t.name] {
			return nil, fmt.Errorf("verification failed for table %s; import rolled back", t.name)
		}
		for _, col := range t.identities {
			var seq string
			if err := tx.QueryRowContext(ctx, "SELECT pg_get_serial_sequence($1,$2)", pgx.Identifier{t.name}.Sanitize(), col).Scan(&seq); err != nil {
				return nil, err
			}
			var next int64
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+pgx.Identifier{col}.Sanitize()+"),0)+1 FROM "+pgx.Identifier{t.name}.Sanitize()).Scan(&next); err != nil {
				return nil, err
			}
			if t.name == "nodes" && next < 2 {
				next = 2
			}
			// ALTER SEQUENCE is transactional, unlike setval: failed restores cannot move
			// a live sequence backwards and cause subsequent ID collisions.
			if _, err := tx.ExecContext(ctx, "ALTER SEQUENCE "+seq+" RESTART WITH "+strconv.FormatInt(next, 10)); err != nil {
				return nil, err
			}
		}
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO mikan_sqlite_import(id,report) VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET report=excluded.report", string(encoded)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := writeMigrationMarker(dir, encoded); err != nil {
		return nil, fmt.Errorf("import committed; retry to recover migration marker: %w", err)
	}
	return report, nil
}

// ConfirmPostgresRestore marks the explicitly restored database as authoritative.
// A fresh-install dump has no SQLite import row, even on a server that once migrated;
// its stale original SQLite must neither block startup nor overwrite the restored data.
func ConfirmPostgresRestore(ctx context.Context, pg *sql.DB, dir string) error {
	var report string
	err := pg.QueryRowContext(ctx, "SELECT report FROM mikan_sqlite_import WHERE id=1").Scan(&report)
	if errors.Is(err, sql.ErrNoRows) {
		encoded, err := json.Marshal(ImportReport{Format: 1, ImportedAt: time.Now().Unix(), SourceKind: "postgresql-restore"})
		if err != nil {
			return err
		}
		report = string(encoded)
		if _, err := pg.ExecContext(ctx, "INSERT INTO mikan_sqlite_import(id,report) VALUES(1,$1)", report); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return writeMigrationMarker(dir, []byte(report))
}

func importSchema(ctx context.Context, sqlite *sql.DB, pg *sql.Tx) ([]importTable, error) {
	rows, err := pg.QueryContext(ctx, "SELECT table_name,column_name,is_identity FROM information_schema.columns WHERE table_schema=current_schema() AND table_name NOT IN ('goose_db_version','mikan_sqlite_import') ORDER BY table_name,ordinal_position")
	if err != nil {
		return nil, err
	}
	lookup := map[string]*importTable{}
	for rows.Next() {
		var name, col, identity string
		if err := rows.Scan(&name, &col, &identity); err != nil {
			rows.Close()
			return nil, err
		}
		if lookup[name] == nil {
			lookup[name] = &importTable{name: name}
		}
		lookup[name].columns = append(lookup[name].columns, col)
		if identity == "YES" {
			lookup[name].identities = append(lookup[name].identities, col)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	names, err := sqlite.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='goose_db_version'")
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			names.Close()
			return nil, err
		}
		if lookup[name] == nil {
			names.Close()
			return nil, fmt.Errorf("unknown SQLite table %s; use a compatible migration version", name)
		}
		found[name] = true
	}
	err = names.Err()
	names.Close()
	if err != nil {
		return nil, err
	}
	dependencies := map[string][]string{}
	for name, t := range lookup {
		if !found[name] {
			return nil, fmt.Errorf("SQLite table %s is missing", name)
		}
		cols, err := sqlite.QueryContext(ctx, "PRAGMA table_info("+pgx.Identifier{name}.Sanitize()+")")
		if err != nil {
			return nil, err
		}
		var columns []string
		keys := map[int]string{}
		t.textKeys = map[string]bool{}
		for cols.Next() {
			var cid, notnull, pk int
			var col, typ string
			var def any
			if err := cols.Scan(&cid, &col, &typ, &notnull, &def, &pk); err != nil {
				cols.Close()
				return nil, err
			}
			columns = append(columns, col)
			if pk > 0 {
				keys[pk] = col
				if strings.EqualFold(typ, "TEXT") {
					t.textKeys[col] = true
				}
			}
		}
		err = cols.Err()
		cols.Close()
		if err != nil {
			return nil, err
		}
		if strings.Join(columns, "\x00") != strings.Join(t.columns, "\x00") {
			return nil, fmt.Errorf("schema mismatch for %s", name)
		}
		for i := 1; i <= len(keys); i++ {
			t.keys = append(t.keys, keys[i])
		}
		if len(t.keys) == 0 {
			return nil, fmt.Errorf("missing primary key for %s", name)
		}
		fks, err := sqlite.QueryContext(ctx, "PRAGMA foreign_key_list("+pgx.Identifier{name}.Sanitize()+")")
		if err != nil {
			return nil, err
		}
		for fks.Next() {
			var id, seq int
			var parent, from, to, onupdate, ondelete, match string
			if err := fks.Scan(&id, &seq, &parent, &from, &to, &onupdate, &ondelete, &match); err != nil {
				fks.Close()
				return nil, err
			}
			if parent != name {
				dependencies[name] = append(dependencies[name], parent)
			}
		}
		err = fks.Err()
		fks.Close()
		if err != nil {
			return nil, err
		}
	}
	var sorted []importTable
	done := map[string]bool{}
	for len(sorted) < len(lookup) {
		var ready []string
		for name := range lookup {
			if done[name] {
				continue
			}
			ok := true
			for _, p := range dependencies[name] {
				if !done[p] {
					ok = false
				}
			}
			if ok {
				ready = append(ready, name)
			}
		}
		if len(ready) == 0 {
			return nil, errors.New("cyclic or missing SQLite foreign keys")
		}
		sort.Strings(ready)
		for _, name := range ready {
			sorted = append(sorted, *lookup[name])
			done[name] = true
		}
	}
	return sorted, nil
}

type queryRows interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func selectTable(t importTable, postgres bool) string {
	quote := func(names []string) string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = pgx.Identifier{n}.Sanitize()
		}
		return strings.Join(out, ",")
	}
	keys := make([]string, len(t.keys))
	for i, k := range t.keys {
		keys[i] = pgx.Identifier{k}.Sanitize()
		if t.textKeys[k] {
			if postgres {
				keys[i] += " COLLATE \"C\""
			} else {
				keys[i] += " COLLATE BINARY"
			}
		}
	}
	return "SELECT " + quote(t.columns) + " FROM " + pgx.Identifier{t.name}.Sanitize() + " ORDER BY " + strings.Join(keys, ",")
}
func hashValues(h hash.Hash, values []any) error {
	for _, v := range values {
		switch x := v.(type) {
		case nil:
			fmt.Fprint(h, "N;")
		case int64:
			fmt.Fprintf(h, "I%d;", x)
		case string:
			fmt.Fprintf(h, "S%d:", len(x))
			h.Write([]byte(x))
		case []byte:
			fmt.Fprintf(h, "S%d:", len(x))
			h.Write(x)
		default:
			return fmt.Errorf("unsupported SQLite value type %T", v)
		}
	}
	fmt.Fprint(h, "R;")
	return nil
}
func scanRow(rows *sql.Rows, n int) ([]any, error) {
	values := make([]any, n)
	dest := make([]any, n)
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	for i, v := range values {
		if b, ok := v.([]byte); ok {
			values[i] = string(b)
		}
	}
	return values, nil
}
func tableProof(ctx context.Context, c queryRows, t importTable) (TableProof, error) {
	rows, err := c.QueryContext(ctx, selectTable(t, true))
	if err != nil {
		return TableProof{}, err
	}
	defer rows.Close()
	h := sha256.New()
	p := TableProof{}
	for rows.Next() {
		values, err := scanRow(rows, len(t.columns))
		if err != nil {
			return p, err
		}
		if err := hashValues(h, values); err != nil {
			return p, err
		}
		p.Rows++
	}
	p.SHA256 = hex.EncodeToString(h.Sum(nil))
	return p, rows.Err()
}

// copyBatch bounds one multi-row INSERT: PostgreSQL takes at most 65535 parameters.
const copyBatch = 500

func copyTable(ctx context.Context, sqlite *sql.DB, pg *sql.Tx, t importTable) (TableProof, error) {
	cols := make([]string, len(t.columns))
	for i, c := range t.columns {
		cols[i] = pgx.Identifier{c}.Sanitize()
	}
	prefix := "INSERT INTO " + pgx.Identifier{t.name}.Sanitize() + "(" + strings.Join(cols, ",") + ") VALUES "
	batch := min(copyBatch, 65535/len(t.columns))
	// Full batches share one statement text, which the driver prepares once.
	insert := func(values []any) error {
		var b strings.Builder
		b.WriteString(prefix)
		for i := range values {
			switch {
			case i == 0:
				b.WriteString("(")
			case i%len(t.columns) == 0:
				b.WriteString("),(")
			default:
				b.WriteString(",")
			}
			b.WriteString("$" + strconv.Itoa(i+1))
		}
		b.WriteString(")")
		_, err := pg.ExecContext(ctx, b.String(), values...)
		return err
	}
	rows, err := sqlite.QueryContext(ctx, selectTable(t, false))
	if err != nil {
		return TableProof{}, err
	}
	defer rows.Close()
	h := sha256.New()
	p := TableProof{}
	pending := make([]any, 0, batch*len(t.columns))
	for rows.Next() {
		values, err := scanRow(rows, len(t.columns))
		if err != nil {
			return p, err
		}
		if err := hashValues(h, values); err != nil {
			return p, err
		}
		pending = append(pending, values...)
		p.Rows++
		if len(pending) == cap(pending) {
			if err := insert(pending); err != nil {
				return p, err
			}
			pending = pending[:0]
		}
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	if len(pending) > 0 {
		if err := insert(pending); err != nil {
			return p, err
		}
	}
	p.SHA256 = hex.EncodeToString(h.Sum(nil))
	return p, nil
}
