package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func newStoreWithMessage(t *testing.T) (*Store, int64) {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	accID, err := st.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	require.NoError(t, err)
	fID, err := st.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	require.NoError(t, err)
	mID, err := st.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})
	require.NoError(t, err)
	return st, mID
}

func mkBlob(t *testing.T, st *Store, sha string) int64 {
	t.Helper()
	id, _, err := st.InsertOrIncBlob(context.Background(), sha, 100, 0)
	require.NoError(t, err)
	return id
}

// TestSetMessageRawBlob_Fresh: empty slot becomes occupied. Result =
// SetFresh, prevBlobID = 0, raw_captured_at recorded.
func TestSetMessageRawBlob_Fresh(t *testing.T) {
	st, mID := newStoreWithMessage(t)
	blobID := mkBlob(t, st, "aa00000000000000000000000000000000000000000000000000000000000000")

	res, prev, err := st.SetMessageRawBlob(context.Background(), mID, blobID, 12345)
	require.NoError(t, err)
	require.Equal(t, SetFresh, res)
	require.Equal(t, int64(0), prev)

	gotBlob, _, found, err := st.GetMessageRawBlob(context.Background(), mID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, blobID, gotBlob)
}

// TestSetMessageRawBlob_NoopOnSameBlob: re-setting the same blob
// returns SetNoop (so the parallel-fetch caller knows to DecBlobRef
// its own dup-bump from InsertOrIncBlob) AND refreshes
// raw_captured_at so the click slides the retention window.
func TestSetMessageRawBlob_NoopOnSameBlob(t *testing.T) {
	st, mID := newStoreWithMessage(t)
	blobID := mkBlob(t, st, "bb00000000000000000000000000000000000000000000000000000000000000")

	_, _, err := st.SetMessageRawBlob(context.Background(), mID, blobID, 100)
	require.NoError(t, err)
	res, prev, err := st.SetMessageRawBlob(context.Background(), mID, blobID, 5000)
	require.NoError(t, err)
	require.Equal(t, SetNoop, res)
	require.Equal(t, int64(0), prev)

	var ts int64
	require.NoError(t, st.DB().QueryRow(
		`SELECT raw_captured_at FROM messages WHERE id = ?`, mID).Scan(&ts))
	require.EqualValues(t, 5000, ts, "SetNoop must still slide the retention window")
}

// TestSetMessageRawBlob_ReplacesDifferent: a new blob displaces the
// old one and the prev id is returned for the caller to DecBlobRef.
func TestSetMessageRawBlob_ReplacesDifferent(t *testing.T) {
	st, mID := newStoreWithMessage(t)
	first := mkBlob(t, st, "cc00000000000000000000000000000000000000000000000000000000000000")
	second := mkBlob(t, st, "dd00000000000000000000000000000000000000000000000000000000000000")

	_, _, err := st.SetMessageRawBlob(context.Background(), mID, first, 100)
	require.NoError(t, err)
	res, prev, err := st.SetMessageRawBlob(context.Background(), mID, second, 200)
	require.NoError(t, err)
	require.Equal(t, SetReplaced, res)
	require.Equal(t, first, prev)

	gotBlob, _, _, err := st.GetMessageRawBlob(context.Background(), mID)
	require.NoError(t, err)
	require.Equal(t, second, gotBlob)
}

// TestGetMessageRawBlob_NullSlot: unset slot reports !found, no error.
func TestGetMessageRawBlob_NullSlot(t *testing.T) {
	st, mID := newStoreWithMessage(t)
	_, _, found, err := st.GetMessageRawBlob(context.Background(), mID)
	require.NoError(t, err)
	require.False(t, found)
}

// TestClearMessageRawBlob_OnSet: clearing a populated slot returns
// the prev blob id and zeros raw_captured_at.
func TestClearMessageRawBlob_OnSet(t *testing.T) {
	st, mID := newStoreWithMessage(t)
	blobID := mkBlob(t, st, "ee00000000000000000000000000000000000000000000000000000000000000")
	_, _, err := st.SetMessageRawBlob(context.Background(), mID, blobID, 100)
	require.NoError(t, err)

	prev, err := st.ClearMessageRawBlob(context.Background(), mID)
	require.NoError(t, err)
	require.NotNil(t, prev)
	require.Equal(t, blobID, *prev)

	_, _, found, _ := st.GetMessageRawBlob(context.Background(), mID)
	require.False(t, found)

	var captured *int64
	require.NoError(t, st.DB().QueryRow(
		`SELECT raw_captured_at FROM messages WHERE id = ?`, mID).Scan(&captured))
	require.Nil(t, captured, "raw_captured_at must be NULL after ClearMessageRawBlob")
}

// TestClearMessageRawBlob_OnNull: clearing an empty slot is a no-op
// returning nil.
func TestClearMessageRawBlob_OnNull(t *testing.T) {
	st, mID := newStoreWithMessage(t)
	prev, err := st.ClearMessageRawBlob(context.Background(), mID)
	require.NoError(t, err)
	require.Nil(t, prev)
}
