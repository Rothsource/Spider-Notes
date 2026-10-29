// Package sqlite implements store.Store on SQLite (modernc.org/sqlite, no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // driver

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
	"github.com/sovannroth/mynotes/migrations"
)

// TimeLayout is the fixed-width UTC text format used for all timestamps.
const TimeLayout = "2006-01-02T15:04:05Z"

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB is the SQLite store.
type DB struct {
	db *sql.DB
	q  querier
	tx bool
}

var _ store.Store = (*DB)(nil)

// Open opens (creating if needed) the database and applies migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := "file:" + url.PathEscape(path) + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection serialises writers, avoiding SQLITE_BUSY lock upgrades.
	// This is a single-user app, so throughput is not a concern.
	sdb.SetMaxOpenConns(1)
	sdb.SetConnMaxLifetime(0)
	d := &DB{db: sdb, q: sdb}
	if err := d.Ping(ctx); err != nil {
		_ = sdb.Close()
		return nil, err
	}
	if err := d.migrate(ctx); err != nil {
		_ = sdb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var n int
		if err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return err
		}
		err = d.InTx(ctx, func(s store.Store) error {
			tx := s.(*DB)
			if _, err := tx.q.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			_, err := tx.q.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, name, ft(time.Now()))
			return err
		})
		if err != nil {
			return err
		}
		slog.Info("applied migration", "version", name)
	}
	return nil
}

// InTx implements store.Store.
func (d *DB) InTx(ctx context.Context, fn func(store.Store) error) error {
	if d.tx {
		return fn(d)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&DB{db: d.db, q: tx, tx: true}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Backup writes a consistent copy of the database using VACUUM INTO.
func (d *DB) Backup(ctx context.Context, path string) error {
	_, err := d.db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

// Ping checks the connection.
func (d *DB) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// ---- helpers ----

// ft formats a time for storage.
func ft(t time.Time) string { return t.UTC().Truncate(time.Second).Format(TimeLayout) }

// fpt formats an optional time.
func fpt(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return ft(*t)
}

func parseTime(v any) (time.Time, bool, error) {
	var s string
	switch x := v.(type) {
	case nil:
		return time.Time{}, false, nil
	case string:
		s = x
	case []byte:
		s = string(x)
	case time.Time:
		return x.UTC(), true, nil
	default:
		return time.Time{}, false, fmt.Errorf("unexpected time type %T", v)
	}
	if s == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	return t.UTC(), err == nil, err
}

type timeScan struct{ t *time.Time }

func (s timeScan) Scan(v any) error {
	t, _, err := parseTime(v)
	*s.t = t
	return err
}

type ptrTimeScan struct{ t **time.Time }

func (s ptrTimeScan) Scan(v any) error {
	t, ok, err := parseTime(v)
	if err != nil {
		return err
	}
	if ok {
		*s.t = &t
	} else {
		*s.t = nil
	}
	return nil
}

type ptrIntScan struct{ p **int64 }

func (s ptrIntScan) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*s.p = nil
	case int64:
		*s.p = &x
	default:
		return fmt.Errorf("unexpected int type %T", v)
	}
	return nil
}

func ts(t *time.Time) timeScan      { return timeScan{t} }
func tsp(t **time.Time) ptrTimeScan { return ptrTimeScan{t} }
func ip(p **int64) ptrIntScan       { return ptrIntScan{p} }
func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}

func checkAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
