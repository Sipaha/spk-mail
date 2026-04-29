package storage

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMessages_InsertAndList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	id, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1,
		MessageID: stringPtr("<a@x>"), Subject: stringPtr("Hello"),
		FromAddr: stringPtr("Bob <b@x.y>"), Date: 1700000000,
		Flags:    `[]`,
		BodyText: stringPtr("hi"),
	})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	got, err := s.GetMessage(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Hello", *got.Subject)
}

// TestFindThreadByMessageIDs_MatchesInReplyTo verifies that the primary
// reference-chain thread lookup attaches a reply to the same thread bucket
// as the parent. This is the path the StoreWriter takes on every message
// insert, so a regression silently splits conversations into singletons.
func TestFindThreadByMessageIDs_MatchesInReplyTo(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	threadID, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "topic", LastDate: 1700000000, MsgCount: 1})

	// Parent message lives in the thread.
	_, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: fID, UID: 1, ThreadID: &threadID,
		MessageID: stringPtr("<parent@x>"), Subject: stringPtr("topic"),
		Date: 1700000000, Flags: `[]`,
	})
	require.NoError(t, err)

	// FindThreadByMessageIDs must return the thread for the parent's id.
	id, ok, err := s.FindThreadByMessageIDs(ctx, []string{"<parent@x>"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, threadID, id)

	// And miss for an id we have not inserted.
	_, ok, err = s.FindThreadByMessageIDs(ctx, []string{"<unknown@x>"})
	require.NoError(t, err)
	require.False(t, ok)

	// Empty input returns ok=false without an error.
	_, ok, err = s.FindThreadByMessageIDs(ctx, nil)
	require.NoError(t, err)
	require.False(t, ok)

	// Multi-id input — production usage passes the full References chain
	// (typically 3-5 ids) and expects to get back the thread of any one
	// that matches. Mix one hit with two misses and assert the parent's
	// thread id is what comes back.
	id, ok, err = s.FindThreadByMessageIDs(ctx,
		[]string{"<unknown1@x>", "<parent@x>", "<unknown2@x>"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, threadID, id)
}

func TestMarkMessagesRead_Batch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Setup: account, folder, two threads, four messages.
	//   m1, m2 — unread in thread T1
	//   m3 — unread in thread T2
	//   m4 — already \Seen in thread T1 (must NOT appear in Changed)
	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)

	// Insert threads via direct SQL (test-only convenience — no public Insert helper).
	threadInsert := func(subj string, lastDate int64) int64 {
		res, err := s.DB().ExecContext(ctx,
			`INSERT INTO threads(subject_norm, last_date, msg_count, unread_count) VALUES (?,?,0,0)`,
			subj, lastDate)
		require.NoError(t, err)
		id, _ := res.LastInsertId()
		return id
	}
	t1 := threadInsert("t1", 100)
	t2 := threadInsert("t2", 200)

	mkMsg := func(uid int64, threadID int64, flags string) int64 {
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: uid,
			ThreadID: &threadID, Flags: flags,
		})
		require.NoError(t, err)
		return id
	}
	m1 := mkMsg(1, t1, `[]`)
	m2 := mkMsg(2, t1, `["\\Flagged"]`)       // unread, has another flag
	m3 := mkMsg(3, t2, `[]`)
	m4 := mkMsg(4, t1, `["\\Seen"]`)           // already seen — must skip

	out, err := s.MarkMessagesRead(ctx, []int64{m1, m2, m3, m4})
	require.NoError(t, err)

	require.Len(t, out.Changed, 3, "m4 was already seen and must be excluded")
	changedIDs := make([]int64, len(out.Changed))
	for i, c := range out.Changed {
		changedIDs[i] = c.MessageID
	}
	require.ElementsMatch(t, []int64{m1, m2, m3}, changedIDs)
	require.ElementsMatch(t, []int64{t1, t2}, out.ChangedThreadIDs,
		"both touched threads must be reported, deduped")

	// DB state: each changed message has \Seen appended; m4 unchanged.
	for _, id := range []int64{m1, m2, m3} {
		row, err := s.GetMessage(ctx, id)
		require.NoError(t, err)
		require.Contains(t, row.Flags, `\Seen`, "m%d should have \\Seen", id)
	}
	row4, err := s.GetMessage(ctx, m4)
	require.NoError(t, err)
	require.Equal(t, `["\\Seen"]`, row4.Flags, "m4 must be byte-identical (no double-seen)")

	// Per-message metadata propagated for IMAP STORE.
	for _, ch := range out.Changed {
		require.Equal(t, accID, ch.AccountID)
		require.Equal(t, folderID, ch.FolderID)
		require.NotNil(t, ch.ThreadID)
	}
}

func TestMarkMessagesRead_EmptyInput(t *testing.T) {
	s := openTestStore(t)
	out, err := s.MarkMessagesRead(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, out.Changed)
	require.Empty(t, out.ChangedThreadIDs)
}

// TestMarkMessagesRead_AtomicityRollback proves that a mid-tx failure rolls
// back EVERY flag update — none of the earlier-in-loop messages stays \Seen.
// Forcing scenario: poison one input row's flags column with malformed JSON
// so json.Unmarshal returns an error after some prior rows in the same tx
// have already been UPDATEd.
func TestMarkMessagesRead_AtomicityRollback(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)

	mkMsg := func(uid int64, flags string) int64 {
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: uid, Flags: flags,
		})
		require.NoError(t, err)
		return id
	}
	good := mkMsg(1, `[]`)
	// Poison: not valid JSON. Insert with valid flags first (InsertMessage
	// doesn't validate), then overwrite the column directly via writeDB to
	// bypass any future validation.
	bad := mkMsg(2, `[]`)
	_, err = s.DB().ExecContext(ctx, `UPDATE messages SET flags = ? WHERE id = ?`, "not-json", bad)
	require.NoError(t, err)

	out, err := s.MarkMessagesRead(ctx, []int64{good, bad})
	require.Error(t, err, "malformed JSON in one row must surface as an error")
	require.Empty(t, out.Changed, "outcome must not leak partial state on rollback")
	require.Empty(t, out.ChangedThreadIDs)

	// The good row must still be unread — the UPDATE inside the rolled-back tx
	// did not commit.
	row, err := s.GetMessage(ctx, good)
	require.NoError(t, err)
	require.Equal(t, `[]`, row.Flags,
		"good message must remain byte-identical — tx rolled back after bad row failed mid-loop")
}

func TestMarkFolderMessagesRead_Batch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)

	t1, err := s.InsertThread(ctx, ThreadRow{SubjectNorm: "t1", LastDate: 100})
	require.NoError(t, err)
	t2, err := s.InsertThread(ctx, ThreadRow{SubjectNorm: "t2", LastDate: 200})
	require.NoError(t, err)

	mkMsg := func(uid int64, threadID int64, flags string) int64 {
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: uid,
			ThreadID: &threadID, Flags: flags,
		})
		require.NoError(t, err)
		return id
	}
	m1 := mkMsg(1, t1, `[]`)
	m2 := mkMsg(2, t1, `["\\Flagged"]`)        // unread + extra flag
	m3 := mkMsg(3, t2, `[]`)
	m4 := mkMsg(4, t1, `["\\Seen"]`)            // already seen — must skip

	out, err := s.MarkFolderMessagesRead(ctx, folderID)
	require.NoError(t, err)

	require.Len(t, out.Changed, 3, "m4 was already seen and must be excluded")
	gotIDs := make([]int64, len(out.Changed))
	for i, c := range out.Changed {
		gotIDs[i] = c.MessageID
	}
	require.ElementsMatch(t, []int64{m1, m2, m3}, gotIDs)
	require.ElementsMatch(t, []int64{t1, t2}, out.ChangedThreadIDs,
		"both touched threads must be reported, deduped")

	for _, id := range []int64{m1, m2, m3} {
		row, err := s.GetMessage(ctx, id)
		require.NoError(t, err)
		require.Contains(t, row.Flags, `\Seen`)
	}
	row4, err := s.GetMessage(ctx, m4)
	require.NoError(t, err)
	require.Equal(t, `["\\Seen"]`, row4.Flags, "m4 must be byte-identical")
}

func TestMarkFolderMessagesRead_AllSeen(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	for i := int64(1); i <= 3; i++ {
		_, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: i, Date: i, Flags: `["\\Seen"]`,
		})
		require.NoError(t, err)
	}

	out, err := s.MarkFolderMessagesRead(ctx, folderID)
	require.NoError(t, err)
	require.Empty(t, out.Changed)
	require.Empty(t, out.ChangedThreadIDs)
}

func TestMarkFolderMessagesRead_EmptyFolder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})

	out, err := s.MarkFolderMessagesRead(ctx, folderID)
	require.NoError(t, err)
	require.Empty(t, out.Changed)
	require.Empty(t, out.ChangedThreadIDs)
}

func TestMarkFolderMessagesRead_FolderScoped(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderA, _ := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	folderB, _ := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "Other", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})

	mkUnread := func(folderID, uid int64) int64 {
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: uid, Flags: `[]`,
		})
		require.NoError(t, err)
		return id
	}
	mkUnread(folderA, 1)
	mkUnread(folderA, 2)
	bMsg1 := mkUnread(folderB, 1)
	bMsg2 := mkUnread(folderB, 2)

	out, err := s.MarkFolderMessagesRead(ctx, folderA)
	require.NoError(t, err)
	require.Len(t, out.Changed, 2)

	for _, id := range []int64{bMsg1, bMsg2} {
		row, err := s.GetMessage(ctx, id)
		require.NoError(t, err)
		require.Equal(t, `[]`, row.Flags, "folder B msg %d should be unchanged", id)
	}
}

// TestMarkFolderMessagesRead_ScanErrorRollback proves the discard-on-error
// invariant when the SELECT itself fails: the returned outcome is empty and
// the good row remains unread. Forcing scenario: poison one row's flags
// column with invalid JSON. The folder-scoped SELECT uses
// `NOT EXISTS json_each(flags) WHERE value='\Seen'` — modernc/sqlite raises
// "malformed JSON" inside the json_each subquery while iterating, surfacing
// as rows.Err() in scanSeenCandidates. So no UPDATE ever executes inside
// the tx; this exercises "scan error → empty outcome", NOT the
// UPDATE-then-rollback path. (For UPDATE-then-rollback specifically, see
// TestMarkMessagesRead_AtomicityRollback above, where the IN-clause SELECT
// materializes both rows before the per-row UPDATE loop runs — and the
// shared markRowsAsSeen helper means coverage on one path covers the other.)
func TestMarkFolderMessagesRead_ScanErrorRollback(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	good, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1, Date: 1, Flags: `[]`,
	})
	require.NoError(t, err)
	bad, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 2, Date: 2, Flags: `[]`,
	})
	require.NoError(t, err)
	_, err = s.DB().ExecContext(ctx, `UPDATE messages SET flags = ? WHERE id = ?`, "not-json", bad)
	require.NoError(t, err)

	out, err := s.MarkFolderMessagesRead(ctx, folderID)
	require.Error(t, err, "malformed JSON in one row must surface as an error")
	require.Empty(t, out.Changed)
	require.Empty(t, out.ChangedThreadIDs)

	row, err := s.GetMessage(ctx, good)
	require.NoError(t, err)
	require.Equal(t, `[]`, row.Flags,
		"good row must remain unread — tx rolled back after bad row failed mid-loop")
}

func TestToggleThreadFlagged_AddOnLatest(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)
	threadID, err := s.InsertThread(ctx, ThreadRow{SubjectNorm: "t", LastDate: 300})
	require.NoError(t, err)
	tid := threadID

	mkMsg := func(uid int64, date int64) int64 {
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: date,
			ThreadID: &tid, Flags: `[]`,
		})
		require.NoError(t, err)
		return id
	}
	m1 := mkMsg(1, 100)
	m2 := mkMsg(2, 200)
	m3 := mkMsg(3, 300) // most recent

	out, err := s.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	require.Equal(t, "added", out.Action)
	require.Len(t, out.Changed, 1)
	require.Equal(t, m3, out.Changed[0].MessageID)
	require.Equal(t, accID, out.Changed[0].AccountID)
	require.Equal(t, folderID, out.Changed[0].FolderID)
	require.Equal(t, int64(3), out.Changed[0].UID)

	row3, _ := s.GetMessage(ctx, m3)
	require.Contains(t, row3.Flags, `\Flagged`)
	row1, _ := s.GetMessage(ctx, m1)
	require.Equal(t, `[]`, row1.Flags)
	row2, _ := s.GetMessage(ctx, m2)
	require.Equal(t, `[]`, row2.Flags)
}

func TestToggleThreadFlagged_RemoveAll(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	threadID, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "t", LastDate: 300})
	tid := threadID

	mkMsg := func(uid int64, date int64, flags string) int64 {
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: date,
			ThreadID: &tid, Flags: flags,
		})
		require.NoError(t, err)
		return id
	}
	m1 := mkMsg(1, 100, `["\\Flagged"]`)
	m2 := mkMsg(2, 200, `["\\Seen"]`)
	m3 := mkMsg(3, 300, `["\\Seen","\\Flagged"]`)

	out, err := s.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	require.Equal(t, "removed", out.Action)
	require.Len(t, out.Changed, 2)
	gotIDs := []int64{out.Changed[0].MessageID, out.Changed[1].MessageID}
	require.ElementsMatch(t, []int64{m1, m3}, gotIDs)

	row1, _ := s.GetMessage(ctx, m1)
	require.NotContains(t, row1.Flags, `\Flagged`)
	row2, _ := s.GetMessage(ctx, m2)
	require.Equal(t, `["\\Seen"]`, row2.Flags)
	row3, _ := s.GetMessage(ctx, m3)
	require.NotContains(t, row3.Flags, `\Flagged`)
	require.Contains(t, row3.Flags, `\Seen`)
}

func TestToggleThreadFlagged_EmptyThread(t *testing.T) {
	s := openTestStore(t)
	out, err := s.ToggleThreadFlagged(context.Background(), 999999)
	require.NoError(t, err)
	require.Equal(t, "noop", out.Action)
	require.Empty(t, out.Changed)
}

func TestToggleThreadFlagged_PreservesOtherFlags(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	threadID, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "t", LastDate: 100})
	tid := threadID

	id, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1, Date: 100,
		ThreadID: &tid, Flags: `["\\Flagged","\\Seen","\\Answered"]`,
	})
	require.NoError(t, err)

	out, err := s.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	require.Equal(t, "removed", out.Action)
	require.Len(t, out.Changed, 1)

	row, _ := s.GetMessage(ctx, id)
	require.Equal(t, `["\\Seen","\\Answered"]`, row.Flags)
}

func TestToggleThreadFlagged_HasFlaggedRefresh(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, _ := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	threadID, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "t", LastDate: 100})
	tid := threadID

	_, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1, Date: 100,
		ThreadID: &tid, Flags: `[]`,
	})
	require.NoError(t, err)

	threads0, _ := s.ListThreads(ctx, ThreadFilter{AccountID: &accID}, 10, 0)
	require.Len(t, threads0, 1)
	require.False(t, threads0[0].HasFlagged)

	_, err = s.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	threads1, _ := s.ListThreads(ctx, ThreadFilter{AccountID: &accID}, 10, 0)
	require.True(t, threads1[0].HasFlagged)

	_, err = s.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	threads2, _ := s.ListThreads(ctx, ThreadFilter{AccountID: &accID}, 10, 0)
	require.False(t, threads2[0].HasFlagged)
}

// TestMarkMessagesRead_NilThreadID covers a message with thread_id IS NULL
// (e.g. mid-flight during sync, before threading runs). The flag flip must
// still happen and the change must appear in out.Changed with ThreadID=nil,
// but no thread-stats refresh fires and ChangedThreadIDs stays empty.
func TestMarkMessagesRead_NilThreadID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)

	// ThreadID left as nil — message exists but isn't threaded yet.
	msgID, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1, Date: 1, Flags: "[]",
	})
	require.NoError(t, err)

	out, err := s.MarkMessagesRead(ctx, []int64{msgID})
	require.NoError(t, err)

	require.Len(t, out.Changed, 1)
	require.Equal(t, msgID, out.Changed[0].MessageID)
	require.Nil(t, out.Changed[0].ThreadID, "no-thread message must propagate ThreadID=nil")
	require.Empty(t, out.ChangedThreadIDs, "no thread → no stats refresh")

	row, err := s.GetMessage(ctx, msgID)
	require.NoError(t, err)
	require.Contains(t, row.Flags, `\Seen`)
}

// TestToggleThreadFlagged_AtomicityRollback proves the discard-on-error
// invariant: any mid-tx failure leaves the outcome empty and rolls back
// whatever writes had landed. The forcing scenario here is a malformed
// flags JSON: the implementation's pre-pass (which decides "added" vs
// "removed") iterates rows in date-DESC order and Unmarshals each one,
// so the poison row aborts the tx during the pre-pass — before any
// UPDATE runs. This exercises the scan-error path, which is the most
// likely real-world fault. The plain UPDATE-then-rollback path is
// covered for the shared mark-read code at TestMarkMessagesRead_AtomicityRollback,
// where the IN-clause SELECT materializes both rows before the per-row
// UPDATE loop runs.
func TestToggleThreadFlagged_AtomicityRollback(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)
	threadID, err := s.InsertThread(ctx, ThreadRow{SubjectNorm: "t", LastDate: 200})
	require.NoError(t, err)
	tid := threadID

	mkMsg := func(uid, date int64, flags string) int64 {
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: date,
			ThreadID: &tid, Flags: flags,
		})
		require.NoError(t, err)
		return id
	}
	good := mkMsg(1, 100, `["\\Flagged"]`)
	bad := mkMsg(2, 200, `["\\Flagged"]`)
	// Poison the bad row's flags column AFTER InsertMessage (which would
	// have rejected the malformed JSON via the json.Marshal-built input
	// path). Direct UPDATE bypasses any future validation.
	_, err = s.DB().ExecContext(ctx, `UPDATE messages SET flags = ? WHERE id = ?`, "not-json", bad)
	require.NoError(t, err)

	out, err := s.ToggleThreadFlagged(ctx, threadID)
	require.Error(t, err, "malformed JSON in one row must surface as an error")
	require.Equal(t, FlagToggleOutcome{}, out, "outcome must be zero on rollback (no Action, no Changed)")

	// The good row's pre-toggle flag must still be present — its UPDATE,
	// even if it ran inside the rolled-back tx, was discarded on the
	// rollback. The poison row stays as we set it.
	rowGood, err := s.GetMessage(ctx, good)
	require.NoError(t, err)
	require.Contains(t, rowGood.Flags, `\Flagged`,
		"good row must retain \\Flagged — the removed-path UPDATE was rolled back")
}
