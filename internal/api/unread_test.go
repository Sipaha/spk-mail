package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	a := newStub(t)
	st := a.Store
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
