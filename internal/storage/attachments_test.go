package storage

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAttachments_InsertAndList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})

	id, err := s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "1.2", Filename: "x.pdf", ContentType: "application/pdf", SizeBytes: 100})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	rows, err := s.ListAttachmentsByMessage(ctx, mID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}
