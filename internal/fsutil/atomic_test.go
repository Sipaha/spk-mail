package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestAtomicWrite_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	require.NoError(t, AtomicWrite(path, []byte("hello"), 0o600))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

func TestAtomicWrite_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	require.NoError(t, AtomicWrite(path, []byte("new"), 0o600))
	got, _ := os.ReadFile(path)
	require.Equal(t, "new", string(got))
}
