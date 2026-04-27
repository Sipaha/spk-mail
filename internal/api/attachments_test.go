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

func TestGetAttachmentLocalPath_FileExists(t *testing.T) {
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

	path := filepath.Join(t.TempDir(), "x.bin")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0o600))
	require.NoError(t, a.Store.UpdateAttachmentDownloaded(ctx, aID, path, "deadbeef", time.Now().Unix()))

	got, err := a.GetAttachmentLocalPath(ctx, aID)
	require.NoError(t, err)
	require.Equal(t, path, got)
}

func TestGetAttachmentLocalPath_FileMissingClearsRow(t *testing.T) {
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

	// Point local_path at a file that doesn't exist.
	missing := filepath.Join(t.TempDir(), "gone.bin")
	require.NoError(t, a.Store.UpdateAttachmentDownloaded(ctx, aID, missing, "deadbeef", time.Now().Unix()))

	_, err = a.GetAttachmentLocalPath(ctx, aID)
	require.ErrorIs(t, err, ErrAttachmentNotReady)
}
