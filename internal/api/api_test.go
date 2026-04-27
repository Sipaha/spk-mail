package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func newStub(t *testing.T) *Stub {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)
	return NewStub(s, sec, NewEmitter(), nil) // nil engine — unit tests don't exercise sync
}

func TestStub_AddListRemoveAccount(t *testing.T) {
	st := newStub(t)
	ctx := context.Background()
	out, err := st.AddAccount(ctx, AddAccountRequest{Name: "A", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", IMAPPassword: "p", UseTLS: true, Color: "#fff"})
	require.NoError(t, err)
	require.Greater(t, out.ID, int64(0))

	list, _ := st.ListAccounts(ctx)
	require.Len(t, list, 1)

	require.NoError(t, st.RemoveAccount(ctx, out.ID))
	list, _ = st.ListAccounts(ctx)
	require.Empty(t, list)
}
