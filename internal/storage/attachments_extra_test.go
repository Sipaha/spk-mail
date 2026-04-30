package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAttachments_PendingThenMarkDownloaded: an attachment without
// blob_id is pending; once UpdateAttachmentDownloaded points it at a
// blob, it disappears from the queue. This is the hot-path the
// AttachmentDownloader relies on each tick.
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

	const sha = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	blobID, isNew, err := s.InsertOrIncBlob(ctx, sha, 100, 1700000000)
	require.NoError(t, err)
	require.True(t, isNew)
	require.NoError(t, s.UpdateAttachmentDownloaded(ctx, aID, blobID, sha, 1700000000))

	pending2, _ := s.ListPendingAttachments(ctx, accID, 10)
	require.Empty(t, pending2, "row with blob_id set must NOT come back as pending")
}

// TestGetAttachmentBlob exercises the read-side path the API stub uses
// to render an attachment-open click: blob_id resolves into a sha that
// the caller composes into a path via BlobPath.
func TestGetAttachmentBlob(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 100, Flags: "[]"})
	aID, _ := s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "1", Filename: "x.pdf", ContentType: "application/pdf", SizeBytes: 100})

	// Not yet downloaded → found=false.
	_, _, found, err := s.GetAttachmentBlob(ctx, aID)
	require.NoError(t, err)
	require.False(t, found)

	const sha = "cafef00dcafef00dcafef00dcafef00dcafef00dcafef00dcafef00dcafef00d"
	blobID, _, err := s.InsertOrIncBlob(ctx, sha, 100, 1700000000)
	require.NoError(t, err)
	require.NoError(t, s.UpdateAttachmentDownloaded(ctx, aID, blobID, sha, 1700000000))

	gotID, gotSha, found, err := s.GetAttachmentBlob(ctx, aID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, blobID, gotID)
	require.Equal(t, sha, gotSha)
}

// TestClearAttachmentBlob_ReturnsPriorBlobID: the file-missing recovery
// path needs the cleared blob_id back so it can DecBlobRef the
// orphaned reference.
func TestClearAttachmentBlob_ReturnsPriorBlobID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 100, Flags: "[]"})
	aID, _ := s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "1", Filename: "x.pdf", ContentType: "application/pdf", SizeBytes: 100})

	const sha = "11111111111111111111111111111111111111111111111111111111111111aa"
	blobID, _, err := s.InsertOrIncBlob(ctx, sha, 100, 1700000000)
	require.NoError(t, err)
	require.NoError(t, s.UpdateAttachmentDownloaded(ctx, aID, blobID, sha, 1700000000))

	prev, err := s.ClearAttachmentBlob(ctx, aID)
	require.NoError(t, err)
	require.NotNil(t, prev)
	require.Equal(t, blobID, *prev)

	// Idempotent: clearing an already-cleared row returns nil prev.
	prev, err = s.ClearAttachmentBlob(ctx, aID)
	require.NoError(t, err)
	require.Nil(t, prev)
}
