package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrate_FreshDBAppliesAllVersions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	defer s.Close()

	var v int
	require.NoError(t, s.DB().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 9, v)

	// profiles table exists
	var name string
	require.NoError(t, s.DB().QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='profiles'`).Scan(&name))
	require.Equal(t, "profiles", name)

	// accounts has profile_id column
	rows, err := s.DB().Query(`PRAGMA table_info(accounts)`)
	require.NoError(t, err)
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var n, typ string
		var notnull, pk int
		var dflt any
		require.NoError(t, rows.Scan(&cid, &n, &typ, &notnull, &dflt, &pk))
		cols[n] = true
	}
	require.True(t, cols["profile_id"], "accounts must have profile_id column after migration")

	// profiles has muted column after v3
	rows2, err := s.DB().Query(`PRAGMA table_info(profiles)`)
	require.NoError(t, err)
	defer rows2.Close()
	pcols := map[string]bool{}
	for rows2.Next() {
		var cid int
		var n, typ string
		var notnull, pk int
		var dflt any
		require.NoError(t, rows2.Scan(&cid, &n, &typ, &notnull, &dflt, &pk))
		pcols[n] = true
	}
	require.True(t, pcols["muted"], "profiles must have muted column after v3")
}

func TestMigrate_PreV2DBGetsBackfillDefaultProfile(t *testing.T) {
	// Simulate a v1-only DB: open, then locally undo the v2 migration so
	// the next Open re-runs it.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	{
		s, err := Open(context.Background(), dbPath)
		require.NoError(t, err)
		_, err = s.DB().Exec(`DELETE FROM schema_migrations WHERE version >= 2`)
		require.NoError(t, err)
		_, err = s.DB().Exec(`DROP TABLE IF EXISTS profiles`)
		require.NoError(t, err)
		// Drop profile_id column by recreating accounts. SQLite < 3.35 cannot
		// DROP COLUMN, so use the table-rebuild idiom (mirrors a real pre-v2 DB).
		_, err = s.DB().Exec(`
			CREATE TABLE accounts_old AS SELECT id,name,email,imap_host,imap_port,imap_username,use_tls,color,created_at FROM accounts;
			DROP TABLE accounts;
			CREATE TABLE accounts (
				id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE,
				imap_host TEXT NOT NULL, imap_port INTEGER NOT NULL, imap_username TEXT NOT NULL,
				use_tls INTEGER NOT NULL, color TEXT NOT NULL, created_at INTEGER NOT NULL
			);
			INSERT INTO accounts SELECT * FROM accounts_old;
			DROP TABLE accounts_old;
		`)
		require.NoError(t, err)
		_, err = s.DB().Exec(`INSERT INTO accounts(name,email,imap_host,imap_port,imap_username,use_tls,color,created_at)
			VALUES ('Old','old@x','h',993,'old@x',1,'#fff',0)`)
		require.NoError(t, err)
		s.Close()
	}

	// Reopen — migrate() should re-run v2.
	s2, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer s2.Close()

	var pcount int
	require.NoError(t, s2.DB().QueryRow(`SELECT COUNT(*) FROM profiles`).Scan(&pcount))
	require.Equal(t, 1, pcount, "exactly one Default profile should be created")

	var pname string
	require.NoError(t, s2.DB().QueryRow(`SELECT name FROM profiles`).Scan(&pname))
	require.Equal(t, "Default", pname)

	var apid *int64
	require.NoError(t, s2.DB().QueryRow(`SELECT profile_id FROM accounts WHERE email='old@x'`).Scan(&apid))
	require.NotNil(t, apid, "existing account must be backfilled to Default profile")
}

// TestMigrationV9_AddsRawCaptureColumns verifies that v9 adds
// raw_blob_id and raw_captured_at to messages along with the partial
// index that backs SweepExpiredRaw. Existing rows must keep working —
// raw_blob_id NULL is the lazy-fetch territory.
func TestMigrationV9_AddsRawCaptureColumns(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	defer st.Close()

	cols := tableColumns(t, st.DB(), "messages")
	require.Contains(t, cols, "raw_blob_id")
	require.Contains(t, cols, "raw_captured_at")

	var sqlText string
	err = st.DB().QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_messages_raw_capture'`,
	).Scan(&sqlText)
	require.NoError(t, err)
	require.Contains(t, sqlText, "raw_captured_at")
	require.Contains(t, sqlText, "WHERE raw_blob_id IS NOT NULL")

	var v int
	require.NoError(t, st.DB().QueryRow(
		`SELECT version FROM schema_migrations WHERE version = 9`).Scan(&v))
	require.Equal(t, 9, v)
}

// tableColumns returns the column names of the given table via PRAGMA.
func tableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	require.NoError(t, err)
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		require.NoError(t, rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk))
		cols = append(cols, name)
	}
	return cols
}
