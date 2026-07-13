package storage

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAccounts_InsertAndGet(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id, err := s.InsertAccount(ctx, AccountRow{
		Name: "X", Email: "x@y.z", IMAPHost: "imap.x", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: time.Now().Unix(),
	})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	got, err := s.GetAccount(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "X", got.Name)
	require.Equal(t, "x@y.z", got.Email)
}

func TestAccounts_ListAll(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := s.InsertAccount(ctx, AccountRow{
			Name: "A", Email: fmt.Sprintf("a%d@x.y", i), IMAPHost: "h", IMAPPort: 993,
			IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: time.Now().Unix(),
		})
		require.NoError(t, err)
	}
	rows, err := s.ListAccounts(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 3)
}

func TestAccounts_Delete(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: time.Now().Unix()})
	require.NoError(t, s.DeleteAccount(ctx, id))
	_, err := s.GetAccount(ctx, id)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestAccounts_Delete_CleansOrphanThreads: threads carry no account FK,
// so the ON DELETE CASCADE on messages.account_id leaves ghost thread
// rows behind (subject_norm set, msg_count now stale, no messages) unless
// DeleteAccount sweeps them in the same tx. Two accounts, each with one
// threaded message: deleting one account must remove exactly its thread
// and leave the other account's thread (and message) untouched.
func TestAccounts_Delete_CleansOrphanThreads(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	acc1, err := s.InsertAccount(ctx, AccountRow{Name: "A", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: time.Now().Unix()})
	require.NoError(t, err)
	f1, err := s.UpsertFolder(ctx, FolderRow{AccountID: acc1, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	require.NoError(t, err)
	_, thread1, err := s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "acc1-topic", LastDate: 1},
		Message:   MessageRow{AccountID: acc1, FolderID: f1, UID: 1, Date: 1, Flags: "[]"},
	})
	require.NoError(t, err)

	acc2, err := s.InsertAccount(ctx, AccountRow{Name: "B", Email: "b@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: time.Now().Unix()})
	require.NoError(t, err)
	f2, err := s.UpsertFolder(ctx, FolderRow{AccountID: acc2, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	require.NoError(t, err)
	_, thread2, err := s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "acc2-topic", LastDate: 2},
		Message:   MessageRow{AccountID: acc2, FolderID: f2, UID: 1, Date: 2, Flags: "[]"},
	})
	require.NoError(t, err)

	require.NoError(t, s.DeleteAccount(ctx, acc1))

	threads, err := s.ListThreadsRecent(ctx, 100, 0)
	require.NoError(t, err)
	require.Len(t, threads, 1, "acc1's orphaned thread must be swept; acc2's thread must survive")
	require.Equal(t, thread2, threads[0].ID)

	var n int
	require.NoError(t, s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM threads WHERE id = ?`, thread1).Scan(&n))
	require.Equal(t, 0, n, "acc1's thread row must actually be gone, not just filtered out")
}
