package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessages_GetByID_AndUpdateFlags(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	id, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]", Subject: stringPtr("hi")})

	got, err := s.GetMessage(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "hi", *got.Subject)

	require.NoError(t, s.UpdateFlags(ctx, id, `["\\Seen"]`))
	got, _ = s.GetMessage(ctx, id)
	require.Equal(t, `["\\Seen"]`, got.Flags)
}

func TestMessages_GetByThread(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	tID, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "x", LastDate: 1, MsgCount: 2})
	for i := 1; i <= 2; i++ {
		_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: int64(i), Date: int64(i), Flags: "[]", ThreadID: &tID})
	}
	rows, err := s.GetMessagesByThread(ctx, tID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
}
