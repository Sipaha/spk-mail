package sync

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

// TestRawSweeper_Once: an explicit sweepOnce call drops expired
// captures and decrements blob refcounts.
func TestRawSweeper_Once(t *testing.T) {
	ctx := context.Background()
	st, err := storage.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	defer st.Close()

	accID, _ := st.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := st.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := st.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})

	const sha = "ab00000000000000000000000000000000000000000000000000000000000000"
	blobID, _, err := st.InsertOrIncBlob(ctx, sha, 100, 0)
	require.NoError(t, err)
	_, _, err = st.SetMessageRawBlob(ctx, mID, blobID, 100)
	require.NoError(t, err)

	sweeper := NewRawSweeper(st, 1*time.Hour)
	cleared := sweeper.sweepOnceAt(ctx, time.Unix(100+3600+1, 0))
	require.Equal(t, 1, cleared)

	_, _, found, _ := st.GetMessageRawBlob(ctx, mID)
	require.False(t, found)

	br, err := st.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 0, br.Refcount)
}
