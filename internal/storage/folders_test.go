package storage

import (
	"context"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestFolders_UpsertAndList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})

	id, err := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: stringPtr("inbox"), UIDValidity: 100, UIDNext: 1})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	// upsert with new uid_next does UPDATE not INSERT
	id2, err := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: stringPtr("inbox"), UIDValidity: 100, UIDNext: 5})
	require.NoError(t, err)
	require.Equal(t, id, id2)

	rows, err := s.ListFolders(ctx, accID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(5), rows[0].UIDNext)
}

func stringPtr(s string) *string { return &s }
