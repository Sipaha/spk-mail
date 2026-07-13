package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBlobPath_FanOut verifies the git-style two-byte fan-out so a
// future tweak to the layout (or a typo introducing extra path
// segments) gets caught.
func TestBlobPath_FanOut(t *testing.T) {
	const sha = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	got := BlobPath("/data", sha)
	require.Equal(t, filepath.Join("/data", "blobs", "ab", "cd", sha), got)
	// Single-letter prefix slices: hex digits only, no path traversal.
	require.False(t, strings.Contains(got, ".."))
}

// TestBlobPath_MalformedSHA: a too-short sha must not silently produce
// a valid-looking path that could collide with a real blob — quarantine
// it under a clearly-named subdir so callers see a clean error from
// os.Open.
func TestBlobPath_MalformedSHA(t *testing.T) {
	got := BlobPath("/data", "abc")
	require.Equal(t, filepath.Join("/data", "blobs", "_invalid", "abc"), got)
}

// TestBlobs_InsertOrInc_Dedupe — the central dedup invariant: inserting
// the SAME sha twice yields one row with refcount=2, and the returned
// blobID is stable across both calls.
func TestBlobs_InsertOrInc_Dedupe(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	id1, isNew1, err := s.InsertOrIncBlob(ctx, "deadbeef00000000000000000000000000000000000000000000000000000000", 100, 1700000000)
	require.NoError(t, err)
	require.True(t, isNew1, "first insert must be flagged isNew so caller knows to write the bytes")

	id2, isNew2, err := s.InsertOrIncBlob(ctx, "deadbeef00000000000000000000000000000000000000000000000000000000", 100, 1700000001)
	require.NoError(t, err)
	require.False(t, isNew2, "second insert with same sha must be flagged !isNew so caller drops its temp file")
	require.Equal(t, id1, id2, "same-sha inserts must return the same blob id")

	got, err := s.GetBlob(ctx, id1)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Refcount, "refcount must reflect both insertions")
	require.EqualValues(t, 100, got.SizeBytes)
}

// TestBlobs_Dec_to_Zero — refcount drops to zero, GC enumerates the row,
// DeleteBlobIfZero removes it. This is the full happy path the GC sweep
// will exercise on every "remove last referencing message" event.
func TestBlobs_Dec_to_Zero(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	const sha = "cafebabe00000000000000000000000000000000000000000000000000000000"
	id, _, err := s.InsertOrIncBlob(ctx, sha, 50, 1700000000)
	require.NoError(t, err)
	id2, _, err := s.InsertOrIncBlob(ctx, sha, 50, 1700000000)
	require.NoError(t, err)
	require.Equal(t, id, id2)

	// First dec: refcount 2 → 1, NOT a candidate yet.
	rc, err := s.DecBlobRef(ctx, id)
	require.NoError(t, err)
	require.EqualValues(t, 1, rc)

	zero, err := s.ListZeroRefBlobs(ctx)
	require.NoError(t, err)
	require.Empty(t, zero, "still referenced; must not appear in GC sweep")

	// Second dec: refcount 1 → 0, becomes a sweep candidate.
	rc, err = s.DecBlobRef(ctx, id)
	require.NoError(t, err)
	require.EqualValues(t, 0, rc)

	zero, err = s.ListZeroRefBlobs(ctx)
	require.NoError(t, err)
	require.Len(t, zero, 1)
	require.Equal(t, sha, zero[0].SHA256)

	deleted, err := s.DeleteBlobIfZero(ctx, id)
	require.NoError(t, err)
	require.True(t, deleted)

	// Row is gone — GetBlob surfaces ErrNotFound, sweep returns empty.
	_, err = s.GetBlob(ctx, id)
	require.True(t, errors.Is(err, ErrNotFound))

	zero, err = s.ListZeroRefBlobs(ctx)
	require.NoError(t, err)
	require.Empty(t, zero)
}

// TestBlobs_DeleteMessages_DecRefcount — the central invariant of the
// GC integration: deleting messages drains the refcount of every blob
// they reference, including the multi-ref case where a single blob is
// pointed at by N attachments inside the same folder. After the
// DELETE, refcount must equal initial - N for that blob (not initial -
// 1 — that would be the bug a per-row UPDATE introduces).
func TestBlobs_DeleteMessages_DecRefcount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	const sha = "0000000000000000000000000000000000000000000000000000000000000001"
	blobID, _, err := s.InsertOrIncBlob(ctx, sha, 100, 1700000000)
	require.NoError(t, err)
	// Bump to refcount=3 by inserting two more attachments that all
	// reference the same blob.
	_, _, _ = s.InsertOrIncBlob(ctx, sha, 100, 1700000000)
	_, _, _ = s.InsertOrIncBlob(ctx, sha, 100, 1700000000)

	// Three attachments in the SAME folder, all pointing at the blob.
	// (Two messages, three attachments — to also exercise the
	// multi-message path of the JOIN.)
	for i := 1; i <= 2; i++ {
		mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: int64(i), Date: int64(i), Flags: "[]"})
		_, _ = s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "1", Filename: "x", ContentType: "x", SizeBytes: 100, BlobID: &blobID})
		if i == 1 {
			_, _ = s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "2", Filename: "y", ContentType: "x", SizeBytes: 100, BlobID: &blobID})
		}
	}

	got, _ := s.GetBlob(ctx, blobID)
	require.EqualValues(t, 3, got.Refcount, "sanity: refcount must be 3 before delete")

	require.NoError(t, s.DeleteMessagesByFolder(ctx, fID))

	got, err = s.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 0, got.Refcount,
		"all 3 references in the folder must drop refcount by exactly 3 (not 1 from per-row dec)")
}

// TestBlobs_DeleteAccount_DecRefcount: same invariant for the
// account-wide drain — a blob referenced from multiple folders of the
// same account must lose every reference at once.
func TestBlobs_DeleteAccount_DecRefcount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	f1, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	f2, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "Sent", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	const sha = "0000000000000000000000000000000000000000000000000000000000000002"
	blobID, _, _ := s.InsertOrIncBlob(ctx, sha, 50, 1700000000)
	_, _, _ = s.InsertOrIncBlob(ctx, sha, 50, 1700000000)

	m1, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: f1, UID: 1, Date: 1, Flags: "[]"})
	m2, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: f2, UID: 1, Date: 2, Flags: "[]"})
	_, _ = s.InsertAttachment(ctx, AttachmentRow{MessageID: m1, PartID: "1", Filename: "x", ContentType: "x", SizeBytes: 50, BlobID: &blobID})
	_, _ = s.InsertAttachment(ctx, AttachmentRow{MessageID: m2, PartID: "1", Filename: "x", ContentType: "x", SizeBytes: 50, BlobID: &blobID})

	got, _ := s.GetBlob(ctx, blobID)
	require.EqualValues(t, 2, got.Refcount)

	require.NoError(t, s.DeleteAccount(ctx, accID))

	got, err := s.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 0, got.Refcount, "both refs across folders must drain")
}

// TestBlobs_DeleteMessages_DecRawBlobRefcount mirrors
// TestBlobs_DeleteMessages_DecRefcount but for the raw-message-blob
// column (messages.raw_blob_id) instead of attachments.blob_id. Two
// messages in the same folder share one raw blob; deleting the folder
// must land refcount at exactly 0 — not -1 from an unguarded double
// decrement, not 1 from a per-row UPDATE that only counts one of the two
// references.
func TestBlobs_DeleteMessages_DecRawBlobRefcount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	const sha = "0000000000000000000000000000000000000000000000000000000000000003"
	blobID, _, err := s.InsertOrIncBlob(ctx, sha, 100, 1700000000)
	require.NoError(t, err)
	// Bump to refcount=2 to match the two messages that will link to it.
	_, _, err = s.InsertOrIncBlob(ctx, sha, 100, 1700000000)
	require.NoError(t, err)

	m1, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 1, Flags: "[]"})
	m2, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 2, Date: 2, Flags: "[]"})
	_, _, err = s.SetMessageRawBlob(ctx, m1, blobID, 1700000000)
	require.NoError(t, err)
	_, _, err = s.SetMessageRawBlob(ctx, m2, blobID, 1700000000)
	require.NoError(t, err)

	got, err := s.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Refcount, "sanity: refcount must be 2 before delete")

	require.NoError(t, s.DeleteMessagesByFolder(ctx, fID))

	got, err = s.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 0, got.Refcount,
		"both raw-blob references in the folder must drop refcount by exactly 2 (not -1, not 1)")
}

// TestBlobs_DeleteAccount_DecRawBlobRefcount: same invariant as
// TestBlobs_DeleteAccount_DecRefcount but for messages.raw_blob_id. A
// raw blob is referenced from messages in two different folders of the
// same account; deleting one folder must drain only its own reference
// (leaving refcount=1), and deleting the account afterward must drain
// the rest (refcount=0).
func TestBlobs_DeleteAccount_DecRawBlobRefcount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	f1, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	f2, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "Sent", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	const sha = "0000000000000000000000000000000000000000000000000000000000000004"
	blobID, _, err := s.InsertOrIncBlob(ctx, sha, 50, 1700000000)
	require.NoError(t, err)
	_, _, err = s.InsertOrIncBlob(ctx, sha, 50, 1700000000)
	require.NoError(t, err)

	m1, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: f1, UID: 1, Date: 1, Flags: "[]"})
	m2, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: f2, UID: 1, Date: 2, Flags: "[]"})
	_, _, err = s.SetMessageRawBlob(ctx, m1, blobID, 1700000000)
	require.NoError(t, err)
	_, _, err = s.SetMessageRawBlob(ctx, m2, blobID, 1700000000)
	require.NoError(t, err)

	got, err := s.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Refcount)

	require.NoError(t, s.DeleteMessagesByFolder(ctx, f1))

	got, err = s.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 1, got.Refcount, "only f1's reference must drain; f2's must survive the folder delete")

	require.NoError(t, s.DeleteAccount(ctx, accID))

	got, err = s.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 0, got.Refcount, "f2's reference must drain when the account is deleted")
}

// TestBlobs_SweepBlobs_UnlinksAndDeletes — full integration: a blob
// row at refcount=0 with a real file on disk gets the file unlinked
// and the row dropped. Live blobs are untouched.
func TestBlobs_SweepBlobs_UnlinksAndDeletes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	dataDir := t.TempDir()

	const orphanSha = "1111111111111111111111111111111111111111111111111111111111111111"
	const liveSha = "2222222222222222222222222222222222222222222222222222222222222222"
	orphanID, _, _ := s.InsertOrIncBlob(ctx, orphanSha, 7, 1700000000)
	liveID, _, _ := s.InsertOrIncBlob(ctx, liveSha, 7, 1700000000)

	// Materialize both files so the unlink has something to remove.
	for _, sha := range []string{orphanSha, liveSha} {
		path := BlobPath(dataDir, sha)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("payload"), 0o600))
	}

	// Drop orphan to refcount=0; live stays at 1.
	_, err := s.DecBlobRef(ctx, orphanID)
	require.NoError(t, err)

	rows, bytes, err := s.SweepBlobs(ctx, dataDir)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows, "exactly one zero-ref blob must be reclaimed")
	require.EqualValues(t, 7, bytes)

	// Orphan: row gone, file gone.
	_, err = s.GetBlob(ctx, orphanID)
	require.True(t, errors.Is(err, ErrNotFound))
	_, err = os.Stat(BlobPath(dataDir, orphanSha))
	require.True(t, os.IsNotExist(err))

	// Live: row + file untouched.
	got, err := s.GetBlob(ctx, liveID)
	require.NoError(t, err)
	require.EqualValues(t, 1, got.Refcount)
	_, err = os.Stat(BlobPath(dataDir, liveSha))
	require.NoError(t, err)
}

// TestBlobs_Dec_RaceResurrect — between sweep enumeration and the
// DeleteBlobIfZero call, a fresh attachment with the same sha can land
// and resurrect the blob. The conditional DELETE must respect the
// refcount > 0 state and refuse to drop the row. This protects the
// invariant that DELETE never removes a blob that has live references.
func TestBlobs_Dec_RaceResurrect(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	const sha = "feedface00000000000000000000000000000000000000000000000000000000"
	id, _, err := s.InsertOrIncBlob(ctx, sha, 10, 1700000000)
	require.NoError(t, err)

	// refcount 1 → 0
	_, err = s.DecBlobRef(ctx, id)
	require.NoError(t, err)

	// Resurrect: another attachment with the same sha lands during the
	// window between sweep enumeration and DeleteBlobIfZero.
	id2, isNew, err := s.InsertOrIncBlob(ctx, sha, 10, 1700000002)
	require.NoError(t, err)
	require.Equal(t, id, id2)
	require.False(t, isNew, "resurrected row reuses the same id")

	deleted, err := s.DeleteBlobIfZero(ctx, id)
	require.NoError(t, err)
	require.False(t, deleted, "must refuse to delete a row that has been resurrected (refcount > 0 now)")

	got, err := s.GetBlob(ctx, id)
	require.NoError(t, err)
	require.EqualValues(t, 1, got.Refcount)
}
