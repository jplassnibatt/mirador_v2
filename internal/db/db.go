// Package db owns the SQLite connection, the migration runner, and all
// queries used by Mirador. A single connection is used deliberately: the app
// is single-user, and one connection keeps PRAGMA settings (notably foreign
// keys) consistent and avoids SQLITE_BUSY between competing writers.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"mirador/internal/migrations"
)

var (
	// ErrNotFound means the requested row does not exist (or is not visible
	// under the requested filter).
	ErrNotFound = errors.New("not found")
	// ErrConflict means a uniqueness or state conflict prevented the change.
	ErrConflict = errors.New("conflict")
	// ErrCrossDate is returned when a drag reorder tries to move a task to a
	// different due date. Rescheduling uses the task edit form instead.
	ErrCrossDate = errors.New("cross-date moves are blocked; edit the task to change its due date")
	// ErrInvalid marks a validation failure.
	ErrInvalid = errors.New("invalid input")
)

// DB wraps the SQLite connection.
type DB struct {
	sql  *sql.DB
	path string
}

// Open opens (and creates if needed) the SQLite database, verifies integrity,
// and applies pending migrations. Existing databases are backed up before a
// migration changes their schema.
func Open(path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	dsn := "file:" + filepath.ToSlash(abs) +
		"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_synchronous=NORMAL"
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	d := &DB{sql: sqlDB, path: abs}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := d.applyPragmas(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := d.IntegrityCheck(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := d.IntegrityCheck(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) applyPragmas() error {
	statements := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	}
	for _, statement := range statements {
		if _, err := d.sql.Exec(statement); err != nil {
			return fmt.Errorf("apply %q: %w", statement, err)
		}
	}
	var mode string
	if err := d.sql.QueryRow("PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return fmt.Errorf("enable WAL mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("enable WAL mode: SQLite reported %q", mode)
	}
	return nil
}

// IntegrityCheck runs SQLite's integrity check and returns an error when the
// database is not healthy.
func (d *DB) IntegrityCheck() error {
	rows, err := d.sql.Query("PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("run integrity check: %w", err)
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("read integrity check: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read integrity check: %w", err)
	}
	if len(results) != 1 || !strings.EqualFold(results[0], "ok") {
		return fmt.Errorf("database integrity check failed: %s", strings.Join(results, "; "))
	}
	return nil
}

func (d *DB) migrate() error {
	if _, err := d.sql.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	if err := d.sql.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	all, err := migrations.All()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	target := current
	for _, m := range all {
		if m.Version > target {
			target = m.Version
		}
	}
	if target <= current {
		return nil
	}

	// Only an existing schema needs a pre-migration backup. A fresh database
	// has nothing worth preserving.
	if current > 0 {
		if err := d.backup(); err != nil {
			return fmt.Errorf("back up database before migration: %w", err)
		}
	}

	for _, m := range all {
		if m.Version <= current {
			continue
		}
		if err := d.applyMigration(m); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) applyMigration(m migrations.Migration) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.Name, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.Name, err)
	}
	if _, err := tx.Exec("INSERT INTO schema_version(version) VALUES (?)", m.Version); err != nil {
		return fmt.Errorf("record migration %s: %w", m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.Name, err)
	}
	if _, err := d.sql.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
		return fmt.Errorf("set user_version after %s: %w", m.Name, err)
	}
	return nil
}

func (d *DB) backup() error {
	if _, err := d.sql.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint WAL: %w", err)
	}
	dir := filepath.Join(filepath.Dir(d.path), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := strings.TrimSuffix(filepath.Base(d.path), filepath.Ext(d.path))
	dest := filepath.Join(dir, fmt.Sprintf("%s-%s.db", base, time.Now().UTC().Format("20060102-150405.000000000")))
	if err := copyFile(d.path, dest); err != nil {
		return err
	}
	return pruneBackups(dir, base, 5)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

func pruneBackups(dir, base string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, base+"-") && strings.HasSuffix(name, ".db") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// deleteStamp returns one high-resolution UTC timestamp for a soft-delete
// cascade. Every row archived by the same operation shares this value, which
// is what lets restore-with-children find exactly the affected rows.
func deleteStamp() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05.000000000")
}

// Close closes the database connection.
func (d *DB) Close() error { return d.sql.Close() }

// Path returns the absolute database path.
func (d *DB) Path() string { return d.path }

// SQL exposes the underlying connection for the export/import package.
func (d *DB) SQL() *sql.DB { return d.sql }

// WithTx runs fn inside a transaction. The transaction is rolled back unless
// fn returns nil.
func (d *DB) WithTx(fn func(*sql.Tx) error) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// IsConflict reports whether err is a SQLite uniqueness violation.
func IsConflict(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
