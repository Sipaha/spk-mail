package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestUnreadCounts_Empty(t *testing.T) {
	a := newStub(t)
	out, err := a.UnreadCounts(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), out.Total)
	require.Empty(t, out.PerAccount)
}

func TestUnreadCounts_WithData(t *testing.T) {
	// Build a stub backed by a real in-memory store so we can seed via Store
	// methods. Mirrors newStub() from api_test.go but keeps the Store handle.
	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)

	a := NewStub(st, sec, NewEmitter(), nil)
	ctx := context.Background()

	accountID, err := st.InsertAccount(ctx, storage.AccountRow{
		Name: "A", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u",
		UseTLS: true, Color: "#fff", CreatedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	inboxRole := "inbox"
	folderID, err := st.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accountID, Name: "INBOX", Delimiter: "/", Role: &inboxRole,
		UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)

	seenFlags, _ := json.Marshal([]string{`\Seen`})
	emptyFlags, _ := json.Marshal([]string{})

	_, err = st.InsertMessage(ctx, storage.MessageRow{
		AccountID: accountID, FolderID: folderID, UID: 1,
		Date: time.Now().Unix(), Flags: string(seenFlags),
	})
	require.NoError(t, err)

	_, err = st.InsertMessage(ctx, storage.MessageRow{
		AccountID: accountID, FolderID: folderID, UID: 2,
		Date: time.Now().Unix(), Flags: string(emptyFlags),
	})
	require.NoError(t, err)

	out, err := a.UnreadCounts(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), out.Total)
	require.Equal(t, int64(1), out.PerAccount[accountID])
}
