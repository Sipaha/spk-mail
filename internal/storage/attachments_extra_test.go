package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttachments_PendingThenMarkDownloaded(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 100, Flags: "[]"})
	aID, _ := s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "1.2", Filename: "x.pdf", ContentType: "application/pdf", SizeBytes: 100})

	pending, err := s.ListPendingAttachments(ctx, accID, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "x.pdf", pending[0].Filename)

	require.NoError(t, s.UpdateAttachmentDownloaded(ctx, aID, "/tmp/x.pdf", "deadbeef", 1700000000))
	pending2, _ := s.ListPendingAttachments(ctx, accID, 10)
	require.Empty(t, pending2)
}

func TestGetAttachmentLocalPath(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 100, Flags: "[]"})
	aID, _ := s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "1", Filename: "x.pdf", ContentType: "application/pdf", SizeBytes: 100})

	// Not yet downloaded -> ("", false, nil).
	path, found, err := s.GetAttachmentLocalPath(ctx, aID)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, "", path)

	// After download -> (path, true, nil).
	require.NoError(t, s.UpdateAttachmentDownloaded(ctx, aID, "/tmp/x.pdf", "deadbeef", 1700000000))
	path, found, err = s.GetAttachmentLocalPath(ctx, aID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "/tmp/x.pdf", path)

	// Cleared again -> ("", false, nil).
	require.NoError(t, s.ClearAttachmentLocalPath(ctx, aID))
	path, found, err = s.GetAttachmentLocalPath(ctx, aID)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, "", path)
}
