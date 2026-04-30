package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestGetAttachmentLocalPath_NotDownloaded(t *testing.T) {
	a := newStub(t)
	ctx := context.Background()
	accID, err := a.Store.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	require.NoError(t, err)
	fID, err := a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	require.NoError(t, err)
	mID, err := a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})
	require.NoError(t, err)
	aID, err := a.Store.InsertAttachment(ctx, storage.AttachmentRow{MessageID: mID, PartID: "1", Filename: "x.bin", ContentType: "application/octet-stream", SizeBytes: 4})
	require.NoError(t, err)

	_, err = a.GetAttachmentLocalPath(ctx, aID)
	require.ErrorIs(t, err, ErrAttachmentNotReady)
}

// TestGetAttachmentLocalPath_FileExists exercises the v7+ blob path:
// stub.DataDir is set so the resolver hits storage.BlobPath.
func TestGetAttachmentLocalPath_FileExists(t *testing.T) {
	a := newStub(t)
	dataDir := t.TempDir()
	a.DataDir = dataDir

	ctx := context.Background()
	accID, _ := a.Store.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})
	aID, _ := a.Store.InsertAttachment(ctx, storage.AttachmentRow{MessageID: mID, PartID: "1", Filename: "x.bin", ContentType: "application/octet-stream", SizeBytes: 4})

	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	blobID, _, err := a.Store.InsertOrIncBlob(ctx, sha, 4, time.Now().Unix())
	require.NoError(t, err)

	// Materialize the file at the expected blob path so the os.Stat
	// in the resolver succeeds.
	path := storage.BlobPath(dataDir, sha)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("data"), 0o600))

	require.NoError(t, a.Store.UpdateAttachmentDownloaded(ctx, aID, blobID, sha, time.Now().Unix()))

	got, err := a.GetAttachmentLocalPath(ctx, aID)
	require.NoError(t, err)
	require.Equal(t, path, got)
}

// TestGetAttachmentLocalPath_FileMissingClearsRow: when the blob file
// vanishes (manual rm, broken backup), the row's blob_id must be
// cleared so the next sweep picks it up as pending and the
// downloader re-fetches.
func TestGetAttachmentLocalPath_FileMissingClearsRow(t *testing.T) {
	a := newStub(t)
	dataDir := t.TempDir()
	a.DataDir = dataDir

	ctx := context.Background()
	accID, _ := a.Store.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})
	aID, _ := a.Store.InsertAttachment(ctx, storage.AttachmentRow{MessageID: mID, PartID: "1", Filename: "x.bin", ContentType: "application/octet-stream", SizeBytes: 4})

	// Point at a blob whose file we never write.
	const sha = "ffeeddccbbaa00112233445566778899aabbccddeeff00112233445566778899"
	blobID, _, err := a.Store.InsertOrIncBlob(ctx, sha, 4, time.Now().Unix())
	require.NoError(t, err)
	require.NoError(t, a.Store.UpdateAttachmentDownloaded(ctx, aID, blobID, sha, time.Now().Unix()))

	_, err = a.GetAttachmentLocalPath(ctx, aID)
	require.ErrorIs(t, err, ErrAttachmentNotReady)

	// Row should now be pending again (blob_id cleared).
	pending, err := a.Store.ListPendingAttachments(ctx, accID, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "row must be queued for re-download after file-missing recovery")
}
