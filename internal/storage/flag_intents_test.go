package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFlagIntents_PersistOverlayAndRevisionAck(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "outbox.sqlite")
	s, err := Open(ctx, path)
	require.NoError(t, err)
	acc, err := s.InsertAccount(ctx, AccountRow{Name: "fixture", Email: "fixture@test", IMAPHost: "localhost", IMAPPort: 143, IMAPUsername: "fixture", Color: "#fff"})
	require.NoError(t, err)
	folder, err := s.UpsertFolder(ctx, FolderRow{AccountID: acc, Name: "INBOX", UIDValidity: 42, UIDNext: 2, HighestModSeq: 0})
	require.NoError(t, err)
	msg, err := s.InsertMessage(ctx, MessageRow{AccountID: acc, FolderID: folder, UID: 1, Flags: `[]`})
	require.NoError(t, err)
	_, err = s.MarkMessagesRead(ctx, []int64{msg})
	require.NoError(t, err)
	first, err := s.ListFlagIntents(ctx, acc, 10)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, int64(42), first[0].UIDValidity)
	require.NoError(t, s.Close())
	s, err = Open(ctx, path)
	require.NoError(t, err)
	defer s.Close()
	// Incoming server flags preserve pending Seen while accepting unrelated flags.
	_, _, _, err = s.UpdateFlagsByUID(ctx, folder, 1, `["\\Answered"]`)
	require.NoError(t, err)
	m, err := s.GetMessage(ctx, msg)
	require.NoError(t, err)
	require.JSONEq(t, `["\\Answered","\\Seen"]`, m.Flags)
	// Explicit unread is a newer decision. Acknowledge the old read only.
	require.NoError(t, s.UpdateFlags(ctx, msg, `["\\Answered"]`))
	require.NoError(t, s.AckFlagIntents(ctx, []int64{first[0].ID}))
	latest, err := s.ListFlagIntents(ctx, acc, 10)
	require.NoError(t, err)
	require.Len(t, latest, 1)
	require.False(t, latest[0].Add)
	require.NotEqual(t, first[0].ID, latest[0].ID)
	_, _, _, err = s.UpdateFlagsByUID(ctx, folder, 1, `["\\Seen","\\Answered","\\Flagged"]`)
	require.NoError(t, err)
	m, err = s.GetMessage(ctx, msg)
	require.NoError(t, err)
	require.JSONEq(t, `["\\Answered","\\Flagged"]`, m.Flags)
	require.NoError(t, s.DeleteMessagesByFolder(ctx, folder))
	latest, err = s.ListFlagIntents(ctx, acc, 10)
	require.NoError(t, err)
	require.Empty(t, latest, "reset purges old epoch intent")
}

func TestFlagIntents_LocalEditAndOutboxAreAtomic(t *testing.T) {
	s, acc, folder := setupFlagIntentStore(t)
	ctx := context.Background()
	msg, err := s.InsertMessage(ctx, MessageRow{AccountID: acc, FolderID: folder, UID: 1, Flags: `[]`})
	require.NoError(t, err)
	_, err = s.DB().Exec(`CREATE TRIGGER reject_intent BEFORE INSERT ON flag_intents BEGIN SELECT RAISE(ABORT,'fixture outbox unavailable'); END`)
	require.NoError(t, err)
	_, err = s.MarkMessagesRead(ctx, []int64{msg})
	require.Error(t, err)
	m, err := s.GetMessage(ctx, msg)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, m.Flags)
}
func setupFlagIntentStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	acc, err := s.InsertAccount(ctx, AccountRow{Name: "fixture", Email: "fixture@test", IMAPHost: "localhost", IMAPPort: 143, IMAPUsername: "fixture", Color: "#fff"})
	require.NoError(t, err)
	folder, err := s.UpsertFolder(ctx, FolderRow{AccountID: acc, Name: "INBOX", UIDValidity: 42})
	require.NoError(t, err)
	return s, acc, folder
}

func TestFlagIntents_ResetKeepsSharedThreadCountsAndResetsWatermark(t *testing.T) {
	s, acc, folder := setupFlagIntentStore(t)
	ctx := context.Background()
	other, err := s.UpsertFolder(ctx, FolderRow{AccountID: acc, Name: "Other", UIDValidity: 9})
	require.NoError(t, err)
	tid, err := s.InsertThread(ctx, ThreadRow{AccountID: acc, SubjectNorm: "shared"})
	require.NoError(t, err)
	for _, f := range []int64{folder, other} {
		_, err = s.InsertMessage(ctx, MessageRow{AccountID: acc, FolderID: f, UID: 1, ThreadID: &tid, Flags: `[]`})
		require.NoError(t, err)
	}
	require.NoError(t, s.UpdateThreadStats(ctx, tid))
	require.NoError(t, s.SetFolderHighestModSeq(ctx, folder, 999))
	_, err = s.UpsertFolder(ctx, FolderRow{AccountID: acc, Name: "INBOX", UIDValidity: 0})
	require.NoError(t, err)
	fs, err := s.ListFolders(ctx, acc)
	require.NoError(t, err)
	require.Equal(t, int64(42), fs[0].UIDValidity, "LIST must not erase the stored epoch")
	require.NoError(t, s.DeleteMessagesByFolder(ctx, folder))
	fs, err = s.ListFolders(ctx, acc)
	require.NoError(t, err)
	require.Zero(t, fs[0].HighestModSeq)
	ts, err := s.ListThreadsRecent(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, ts, 1)
	require.Equal(t, int64(1), ts[0].MsgCount)
	require.Equal(t, int64(1), ts[0].UnreadCount)
}
