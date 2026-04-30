package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBackfill_HappyPath: a row that pre-dates v7 (blob_id NULL,
// local_path points at a real file) is migrated into the blob store.
// After the call, the row references a blob, the file at the legacy
// path is gone, and the blob lives at BlobPath(dataDir, sha).
func TestBackfill_HappyPath(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	dataDir := t.TempDir()
	legacyDir := t.TempDir()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})

	// Materialize a legacy file and insert a row pointing at it.
	legacyPath := filepath.Join(legacyDir, "x.bin")
	require.NoError(t, os.WriteFile(legacyPath, []byte("legacy payload"), 0o600))
	lp := legacyPath
	aID, _ := s.InsertAttachment(ctx, AttachmentRow{
		MessageID: mID, PartID: "1", Filename: "x.bin",
		ContentType: "application/octet-stream", SizeBytes: 14,
		LocalPath: &lp,
	})

	migrated, err := s.BackfillLegacyAttachments(ctx, dataDir)
	require.NoError(t, err)
	require.Equal(t, 1, migrated)

	// Row now points at a blob; legacy local_path is null.
	blobID, sha, found, err := s.GetAttachmentBlob(ctx, aID)
	require.NoError(t, err)
	require.True(t, found)
	require.NotZero(t, blobID)
	require.Len(t, sha, 64)

	// Legacy file removed; blob file at the new path.
	_, err = os.Stat(legacyPath)
	require.True(t, os.IsNotExist(err))

	bytes, err := os.ReadFile(BlobPath(dataDir, sha))
	require.NoError(t, err)
	require.Equal(t, []byte("legacy payload"), bytes)
}

// TestBackfill_Idempotent: a second call after a successful migration
// is a no-op (no candidates found, no double-incremented refcounts).
func TestBackfill_Idempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	dataDir := t.TempDir()
	legacyDir := t.TempDir()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})
	legacyPath := filepath.Join(legacyDir, "x.bin")
	require.NoError(t, os.WriteFile(legacyPath, []byte("payload"), 0o600))
	lp := legacyPath
	_, _ = s.InsertAttachment(ctx, AttachmentRow{
		MessageID: mID, PartID: "1", Filename: "x.bin",
		ContentType: "x", SizeBytes: 7, LocalPath: &lp,
	})

	m1, _ := s.BackfillLegacyAttachments(ctx, dataDir)
	require.Equal(t, 1, m1)

	// Second pass — nothing left to do.
	m2, err := s.BackfillLegacyAttachments(ctx, dataDir)
	require.NoError(t, err)
	require.Equal(t, 0, m2)

	// Refcount must remain at 1, not 2 (would be the bug a non-
	// idempotent backfill introduces).
	var refcount int
	require.NoError(t, s.DB().QueryRow(`SELECT refcount FROM blobs LIMIT 1`).Scan(&refcount))
	require.Equal(t, 1, refcount)
}

// TestBackfill_DedupesAcrossLegacyRows: two legacy rows pointing at
// byte-identical files end up sharing one blob with refcount=2 — the
// classic value-add of moving to content addressing.
func TestBackfill_DedupesAcrossLegacyRows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	dataDir := t.TempDir()
	legacyDir := t.TempDir()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	for i := 1; i <= 2; i++ {
		mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: int64(i), Date: int64(i), Flags: "[]"})
		legacyPath := filepath.Join(legacyDir, "logo-msg.bin")
		// Same byte content for both, different paths (per-message tree).
		legacyPath = filepath.Join(legacyDir, "msg-"+itoa(i)+"-logo.bin")
		require.NoError(t, os.WriteFile(legacyPath, []byte("identical payload"), 0o600))
		lp := legacyPath
		_, _ = s.InsertAttachment(ctx, AttachmentRow{
			MessageID: mID, PartID: "1", Filename: "logo.bin",
			ContentType: "image/png", SizeBytes: 17, LocalPath: &lp,
		})
	}

	migrated, err := s.BackfillLegacyAttachments(ctx, dataDir)
	require.NoError(t, err)
	require.Equal(t, 2, migrated)

	var nBlobs, refcount int
	require.NoError(t, s.DB().QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&nBlobs))
	require.NoError(t, s.DB().QueryRow(`SELECT refcount FROM blobs LIMIT 1`).Scan(&refcount))
	require.Equal(t, 1, nBlobs, "identical payloads must collapse to one blob")
	require.Equal(t, 2, refcount, "refcount counts both attachments")
}

// TestBackfill_MissingFile: a row whose local_path points at a file
// that's gone falls back into pending state (blob_id NULL,
// local_path NULL) so the downloader re-fetches.
func TestBackfill_MissingFile(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	dataDir := t.TempDir()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})

	gonePath := filepath.Join(t.TempDir(), "gone.bin")
	lp := gonePath
	aID, _ := s.InsertAttachment(ctx, AttachmentRow{
		MessageID: mID, PartID: "1", Filename: "gone.bin",
		ContentType: "x", SizeBytes: 0, LocalPath: &lp,
	})

	migrated, err := s.BackfillLegacyAttachments(ctx, dataDir)
	require.NoError(t, err)
	require.Equal(t, 0, migrated, "missing-file rows must NOT count as migrated")

	pending, err := s.ListPendingAttachments(ctx, accID, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "row must fall back to pending so the downloader re-fetches")
	require.Equal(t, aID, pending[0].AttachmentID)
}

// itoa is a local helper used only inside this test file to keep
// imports minimal. Not exposed as it duplicates strconv.Itoa.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
