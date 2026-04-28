package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpen_CreatesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer s.Close()

	var v int
	err = s.DB().QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&v)
	require.NoError(t, err)
	require.Equal(t, 5, v)

	// Tables exist
	for _, tbl := range []string{"accounts", "folders", "messages", "threads", "attachments", "messages_fts"} {
		var name string
		err := s.DB().QueryRow("SELECT name FROM sqlite_master WHERE name = ?", tbl).Scan(&name)
		require.NoError(t, err, "table %q should exist", tbl)
	}
}

func TestOpen_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s1, err := Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	s2, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer s2.Close()
	var v int
	require.NoError(t, s2.DB().QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&v))
	require.Equal(t, 5, v)
}
