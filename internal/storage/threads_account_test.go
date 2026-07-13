package storage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mkAccountWithInbox(t *testing.T, s *Store, name, email string) (accountID, folderID int64) {
	t.Helper()
	ctx := context.Background()
	acc, err := s.InsertAccount(ctx, AccountRow{
		Name: name, Email: email, IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: time.Now().Unix(),
	})
	require.NoError(t, err)
	f, err := s.UpsertFolder(ctx, FolderRow{AccountID: acc, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	require.NoError(t, err)
	return acc, f
}

// TestListThreads_AccountIDIsNewestMessage: the unified "All mail" list colours
// each row by its account, so ListThreads carries one. Threads are NOT
// account-scoped in the schema — a thread can hold messages from two accounts —
// so the contract is specifically "the account of the newest message", and this
// pins it: a thread whose latest message belongs to account B must report B
// even though it started in account A.
func TestListThreads_AccountIDIsNewestMessage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accA, folderA := mkAccountWithInbox(t, s, "A", "a@x")
	accB, folderB := mkAccountWithInbox(t, s, "B", "b@x")

	// Single-account thread.
	_, soloThread, err := s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "solo", LastDate: 10},
		Message:   MessageRow{AccountID: accA, FolderID: folderA, UID: 1, Date: 10, Flags: "[]"},
	})
	require.NoError(t, err)

	// Cross-account thread: starts in A (older), newest message is in B.
	_, mixedThread, err := s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "mixed", LastDate: 20},
		Message:   MessageRow{AccountID: accA, FolderID: folderA, UID: 2, Date: 20, Flags: "[]"},
	})
	require.NoError(t, err)
	_, _, err = s.InsertParsedMessageBundle(ctx, MessageBundle{
		ExistingThreadID: mixedThread,
		Message:          MessageRow{AccountID: accB, FolderID: folderB, UID: 1, Date: 30, Flags: "[]"},
	})
	require.NoError(t, err)

	threads, err := s.ListThreadsRecent(ctx, 100, 0)
	require.NoError(t, err)

	byID := map[int64]ThreadRow{}
	for _, th := range threads {
		byID[th.ID] = th
	}
	require.Equal(t, accA, byID[soloThread].AccountID)
	require.Equal(t, accB, byID[mixedThread].AccountID,
		"a thread must report the account of its NEWEST message, not the one it started in")
}
