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
