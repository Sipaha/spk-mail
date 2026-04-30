package storage

import (
	"context"
	"errors"
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
