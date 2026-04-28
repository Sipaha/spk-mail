package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindThreadBySubject_HitAndMiss(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "A", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	tID, err := s.InsertThread(ctx, ThreadRow{SubjectNorm: "weekly report", LastDate: 1_700_000_000, MsgCount: 1})
	require.NoError(t, err)
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 1_700_000_000, Flags: `[]`, ThreadID: &tID})

	// Hit: exact subject within ±14 days, same account.
	id, ok, err := s.FindThreadBySubject(ctx, accID, "weekly report", 1_700_000_000+86400, 14*86400)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, tID, id)

	// Miss: outside window.
	id, ok, err = s.FindThreadBySubject(ctx, accID, "weekly report", 1_700_000_000+30*86400, 14*86400)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, int64(0), id)

	// Miss: empty subject is never matched.
	id, ok, err = s.FindThreadBySubject(ctx, accID, "", 1_700_000_000, 14*86400)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, int64(0), id)

	// Miss: different subject.
	id, ok, err = s.FindThreadBySubject(ctx, accID, "unrelated", 1_700_000_000, 14*86400)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, int64(0), id)
}

// TestFindThreadBySubject_NoCrossAccountMerge verifies the account-scope
// guard: the same normalised subject within the date window in account B
// must NOT be returned as a candidate when the lookup is for account A.
// Without scoping, two unrelated "Re: Newsletter" messages in different
// accounts merge into one thread and surface as cross-account leakage in
// the UI.
func TestFindThreadBySubject_NoCrossAccountMerge(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accA, _ := s.InsertAccount(ctx, AccountRow{Name: "A", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	accB, _ := s.InsertAccount(ctx, AccountRow{Name: "B", Email: "b@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#000"})
	fA, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accA, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	fB, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accB, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	// Account B has a thread for "newsletter".
	tB, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "newsletter", LastDate: 1_700_000_000})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accB, FolderID: fB, UID: 1, Date: 1_700_000_000, Flags: `[]`, ThreadID: &tB})

	// Lookup from account A's perspective: must miss, not merge.
	id, ok, err := s.FindThreadBySubject(ctx, accA, "newsletter", 1_700_000_000, 14*86400)
	require.NoError(t, err)
	require.False(t, ok, "subject lookup should be account-scoped")
	require.Equal(t, int64(0), id)

	// And account A's own thread is still findable from account A.
	tA, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "newsletter", LastDate: 1_700_000_000})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accA, FolderID: fA, UID: 1, Date: 1_700_000_000, Flags: `[]`, ThreadID: &tA})
	id, ok, err = s.FindThreadBySubject(ctx, accA, "newsletter", 1_700_000_000, 14*86400)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, tA, id)
}

func TestFindThreadBySubject_MostRecent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	older, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "topic", LastDate: 1_000_000})
	newer, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "topic", LastDate: 1_500_000})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 1_000_000, Flags: `[]`, ThreadID: &older})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 2, Date: 1_500_000, Flags: `[]`, ThreadID: &newer})

	id, ok, err := s.FindThreadBySubject(ctx, accID, "topic", 1_300_000, 14*86400)
	require.NoError(t, err)
	require.True(t, ok)
	// ORDER BY last_date DESC — should return newer.
	require.Equal(t, newer, id)
	require.NotEqual(t, older, id)
}

func TestUpdateThreadStats_FlagPatternsTight(t *testing.T) {
	// Verifies the json_each-based flag check matches real flags but NOT
	// substrings hiding inside other JSON values (the previous LIKE-pattern
	// implementation used '%"\\Seen"%' and would falsely match "\\Seenmaybe").
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
