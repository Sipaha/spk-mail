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
