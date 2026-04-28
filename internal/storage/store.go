package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // register sqlite driver
)

//go:embed schema.sql
var schemaSQL string

// readPoolSize bounds the read connection pool. Realistic concurrent readers
// from the UI (filter switch + SSE-driven refetches + click GetThread + startup
// listAccounts/listProfiles + occasional search) stays well under 16; the
// ceiling is paranoia headroom, not a measured target. SQLite WAL imposes no
// architectural limit on concurrent readers.
const readPoolSize = 16

type Store struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

// Open creates the database file (if missing), runs migrations on the writer
// connection, then opens the read pool. The writer is opened first so it can
// initialize WAL mode and create the -wal/-shm sidecar files; opening the
// reader first with mode=ro against a brand-new database would fail.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}

	writeDSN := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)"
	writeDB, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, err
	}
	writeDB.SetMaxOpenConns(1)
	if err := writeDB.PingContext(ctx); err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	if err := migrate(ctx, writeDB); err != nil {
		_ = writeDB.Close()
		return nil, err
	}

	// mode=ro: OS-level read-only file descriptor. query_only=true: PRAGMA that
	// rejects mutations on this connection. Either alone suffices; both make a
	// misroute (storage method picks readDB for a write) fail at the driver
	// rather than silently succeeding on a different connection.
	readDSN := "file:" + path +
		"?_pragma=query_only(true)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)" +
		"&mode=ro"
	readDB, err := sql.Open("sqlite", readDSN)
	if err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(readPoolSize)
	readDB.SetMaxIdleConns(readPoolSize)
	if err := readDB.PingContext(ctx); err != nil {
		_ = writeDB.Close()
		_ = readDB.Close()
		return nil, err
	}
	return &Store{readDB: readDB, writeDB: writeDB}, nil
}

// DB returns the writer connection. Kept for back-compat with tests that
// inspect schema_migrations directly. Do not use this for new code — go
// through the typed methods.
func (s *Store) DB() *sql.DB { return s.writeDB }

// Close closes both connections. Returns the first non-nil error, but always
// attempts to close both.
func (s *Store) Close() error {
	var e1, e2 error
	if s.readDB != nil {
		e1 = s.readDB.Close()
	}
	if s.writeDB != nil {
		e2 = s.writeDB.Close()
	}
	if e1 != nil {
		return e1
	}
	return e2
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for _, m := range migrationSteps {
		if m.version <= current {
			continue
		}
		if err := m.apply(ctx, db); err != nil {
			return fmt.Errorf("migration v%d: %w", m.version, err)
		}
	}
	return nil
}
