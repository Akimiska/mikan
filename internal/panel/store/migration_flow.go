package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
)

// SQLite's final normalized schema matches PostgreSQL baseline 1. A user may skip
// the first PostgreSQL release: importing must precede newer PostgreSQL-only schema
// changes, otherwise required new columns/tables would make that import impossible.
const sqliteImportBaseline int64 = 1

// PreparePostgresImport creates only the import baseline on an empty destination.
// Existing PostgreSQL installations retain their current schema: this never rolls
// schema versions backwards, including on a repeated or interrupted migration.
func PreparePostgresImport(ctx context.Context, dsn string) (*sql.DB, error) {
	conn, err := connectPostgres(ctx, dsn)
	if err != nil {
		return nil, err
	}
	err = recoverRestore(ctx, conn)
	if err == nil {
		var fsys fs.FS
		if fsys, err = fs.Sub(postgresMigrations, "postgres"); err == nil {
			err = preparePostgresImport(ctx, conn, fsys)
		}
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func preparePostgresImport(ctx context.Context, conn *sql.DB, fsys fs.FS) error {
	p, err := postgresProvider(ctx, conn, fsys)
	if err != nil {
		return err
	}
	if _, err := p.UpTo(ctx, sqliteImportBaseline); err != nil {
		return fmt.Errorf("PostgreSQL import baseline: %w", err)
	}
	return nil
}

// FinishPostgresImport applies PostgreSQL migrations only after the import commits
// and verifies. On a retry, ImportSQLite reads its committed proof instead of copying
// stale SQLite over new PostgreSQL data; pending migrations can then continue.
func FinishPostgresImport(ctx context.Context, conn *sql.DB) error {
	return migratePostgres(ctx, conn)
}

func finishPostgresImport(ctx context.Context, conn *sql.DB, fsys fs.FS) error {
	p, err := postgresProvider(ctx, conn, fsys)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("PostgreSQL migrations: %w", err)
	}
	return nil
}
