// Package teststore opens isolated on-disk stores for tests without importing api.
package teststore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// Open opens an isolated SQLite store + secrets file for tests.
func Open(t *testing.T) (*storage.Store, *secrets.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	if err != nil {
		t.Fatal(err)
	}
	return st, sec
}
