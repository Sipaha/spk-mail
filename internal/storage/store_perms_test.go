package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOpen_DBFileIsOwnerOnly pins that the SQLite file holding message bodies
// and encrypted account rows is created 0600, not left at the driver's
// umask-dependent default.
func TestOpen_DBFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer s.Close()

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(),
		"db.sqlite must be owner-only (0600)")
}
