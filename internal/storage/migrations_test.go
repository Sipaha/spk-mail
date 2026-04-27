package storage

import (
	"context"
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
	require.Equal(t, 3, v)

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
