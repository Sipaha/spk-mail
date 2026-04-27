// Package testapi provides constructors for tests in other packages.
package testapi

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

func NewStub(t *testing.T) api.API {
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
	return api.NewStub(st, sec, api.NewEmitter(), nil)
}
