package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindThreadBySubject_HitAndMiss(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tID, err := s.InsertThread(ctx, ThreadRow{SubjectNorm: "weekly report", LastDate: 1_700_000_000, MsgCount: 1})
	require.NoError(t, err)

	// Hit: exact subject within ±14 days.
	id, ok, err := s.FindThreadBySubject(ctx, "weekly report", 1_700_000_000+86400, 14*86400)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, tID, id)

	// Miss: outside window.
	id, ok, err = s.FindThreadBySubject(ctx, "weekly report", 1_700_000_000+30*86400, 14*86400)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, int64(0), id)

	// Miss: empty subject is never matched.
	id, ok, err = s.FindThreadBySubject(ctx, "", 1_700_000_000, 14*86400)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, int64(0), id)

	// Miss: different subject.
	id, ok, err = s.FindThreadBySubject(ctx, "unrelated", 1_700_000_000, 14*86400)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, int64(0), id)
}

func TestFindThreadBySubject_MostRecent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	older, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "topic", LastDate: 1_000_000})
	newer, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "topic", LastDate: 1_500_000})

	id, ok, err := s.FindThreadBySubject(ctx, "topic", 1_300_000, 14*86400)
	require.NoError(t, err)
	require.True(t, ok)
	// ORDER BY last_date DESC — should return newer.
	require.Equal(t, newer, id)
	require.NotEqual(t, older, id)
}

func TestUpdateThreadStats_FlagPatternsTight(t *testing.T) {
	// Verifies the LIKE patterns match real flags but NOT substrings hiding
	// inside other JSON values.
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	tID, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "x", LastDate: 1, MsgCount: 0})

	// One message: real \Seen + \Flagged.
	_, _ = s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: fID, UID: 1, Date: 1,
		Flags: `["\\Seen","\\Flagged"]`, ThreadID: &tID,
	})
	// Another: a hostile flag value containing the substring "Seen" but not
	// the actual flag. With the old LIKE pattern this would have falsely
	// counted as seen.
	_, _ = s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: fID, UID: 2, Date: 2,
		Flags: `["\\Seenmaybe"]`, ThreadID: &tID,
	})

	require.NoError(t, s.UpdateThreadStats(ctx, tID))

	rows, err := s.ListThreadsRecent(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	// 2 messages total, 1 truly unread (the hostile one).
	require.Equal(t, int64(2), rows[0].MsgCount)
	require.Equal(t, int64(1), rows[0].UnreadCount)
	require.True(t, rows[0].HasFlagged)
}
