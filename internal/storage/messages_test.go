package storage

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMessages_InsertAndList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	id, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1,
		MessageID: stringPtr("<a@x>"), Subject: stringPtr("Hello"),
		FromAddr: stringPtr("Bob <b@x.y>"), Date: 1700000000,
		Flags:    `[]`,
		BodyText: stringPtr("hi"),
	})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	rows, err := s.ListMessagesByFolder(ctx, folderID, 100, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Hello", *rows[0].Subject)
}
