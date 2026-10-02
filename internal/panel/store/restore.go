package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"

	"github.com/jackc/pgx/v5"
)

// A restore replaces the whole schema, not only the objects its archive names: a backup
// made before a newer migration would otherwise leave that migration's tables behind
// (pg_restore --clean drops only what the archive contains) or meet columns it never had.
const restoreSuffix = "_pre_restore"

// RestoreSQLite replaces the application data with a legacy SQLite backup. The backup is
// imported at the SQLite baseline and then migrated, like a first upgrade.
func RestoreSQLite(ctx context.Context, dsn, dir, source string) error {
	return restoreSchema(ctx, dsn, func(ctx context.Context) error {
		pg, err := connectPostgres(ctx, dsn)
		if err != nil {
			return err
		}
		defer pg.Close()
		fsys, err := fs.Sub(postgresMigrations, "postgres")
		if err != nil {
			return err
		}
		if err := preparePostgresImport(ctx, pg, fsys); err != nil {
			return err
		}
		if _, err := ImportSQLiteRestore(ctx, pg, dir, source); err != nil {
			return err
		}
		return finishPostgresImport(ctx, pg, fsys)
	})
}

// RestorePostgres replaces the application data with what pgRestore loads into an empty
// schema, then migrates it to this binary's schema.
func RestorePostgres(ctx context.Context, dsn, dir string, pgRestore func(context.Context) error) error {
	return restoreSchema(ctx, dsn, func(ctx context.Context) error {
		if err := pgRestore(ctx); err != nil {
			return err
		}
		pg, err := connectPostgres(ctx, dsn)
		if err != nil {
			return err
		}
		defer pg.Close()
		if err := migratePostgres(ctx, pg); err != nil {
			return err
		}
		return ConfirmPostgresRestore(ctx, pg, dir)
	})
}

// restoreSchema runs load against a fresh, empty schema under the current schema's name.
// The previous schema waits aside until load succeeds; a failure brings it back, and so
// does the next start after a crash (an unfinished restore never reported success).
func restoreSchema(ctx context.Context, dsn string, load func(context.Context) error) error {
	conn, err := connectPostgres(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close()
	// One session holds the restore lock for the whole restore.
	c, err := conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	schema, aside, lockID, err := restoreNames(ctx, c)
	if err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return err
	}
	defer c.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID)
	if err := revertRestore(ctx, c, schema, aside); err != nil {
		return fmt.Errorf("revert an unfinished restore: %w", err)
	}
	if err := inTx(ctx, c, "ALTER SCHEMA "+quote(schema)+" RENAME TO "+quote(aside), "CREATE SCHEMA "+quote(schema)); err != nil {
		return err
	}
	if err := load(ctx); err != nil {
		if back := revertRestore(context.WithoutCancel(ctx), c, schema, aside); back != nil {
			return errors.Join(err, fmt.Errorf("the previous data stay in schema %s: %w", aside, back))
		}
		return err
	}
	if _, err := c.ExecContext(ctx, "DROP SCHEMA "+quote(aside)+" CASCADE"); err != nil {
		return fmt.Errorf("restore finished, the previous data stay in schema %s: %w", aside, err)
	}
	return nil
}

// recoverRestore puts back the schema an interrupted restore set aside. It refuses to
// run beside a restore in progress.
func recoverRestore(ctx context.Context, conn *sql.DB) error {
	c, err := conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	schema, aside, lockID, err := restoreNames(ctx, c)
	if err != nil {
		return err
	}
	var locked bool
	if err := c.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errors.New("a database restore is running; start after it ends")
	}
	defer c.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID)
	if err := revertRestore(ctx, c, schema, aside); err != nil {
		return fmt.Errorf("revert an unfinished restore: %w", err)
	}
	return nil
}

func restoreNames(ctx context.Context, c *sql.Conn) (schema, aside string, lockID int64, err error) {
	var database string
	if err = c.QueryRowContext(ctx, "SELECT current_database(),current_schema()").Scan(&database, &schema); err != nil {
		return
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "mikan-restore:%s:%s", database, schema)
	return schema, schema + restoreSuffix, int64(h.Sum64()), nil
}

func revertRestore(ctx context.Context, c *sql.Conn, schema, aside string) error {
	var exists bool
	if err := c.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname=$1)", aside).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return inTx(ctx, c, "DROP SCHEMA IF EXISTS "+quote(schema)+" CASCADE", "ALTER SCHEMA "+quote(aside)+" RENAME TO "+quote(schema))
}

func inTx(ctx context.Context, c *sql.Conn, statements ...string) error {
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, s := range statements {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func quote(name string) string { return pgx.Identifier{name}.Sanitize() }

// SchemaVersion is the applied PostgreSQL migration version.
func SchemaVersion(ctx context.Context, conn *sql.DB) (int64, error) {
	fsys, err := fs.Sub(postgresMigrations, "postgres")
	if err != nil {
		return 0, err
	}
	p, err := postgresProvider(ctx, conn, fsys)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}
