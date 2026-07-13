package api

import (
	"context"
	"testing"

	"github.com/spk/spk-mail/internal/teststore"
	"github.com/stretchr/testify/require"
)

// addAccountTo inserts one account and returns its id.
func addAccountTo(t *testing.T, s *Stub) int64 {
	t.Helper()
	out, err := s.AddAccount(context.Background(), AddAccountRequest{
		Name: "A", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", IMAPPassword: "p", UseTLS: true, Color: "#fff",
	})
	require.NoError(t, err)
	return out.ID
}

// TestListAccounts_StatusComesFromEngine pins the contract that replaced a
// synthetic "ok": the status shown is the one the account's worker last
// published, and an account nobody has reported on yet is "connecting".
// Reporting "ok" there hid a broken account for as long as the supervise
// backoff — up to 300s.
func TestListAccounts_StatusComesFromEngine(t *testing.T) {
	ctx := context.Background()

	t.Run("worker reported an error: status and reason are surfaced", func(t *testing.T) {
		st, sec := teststore.Open(t)
		eng := &spyEngine{worker: &spyWorker{}, status: "error", detail: "dial tcp: connection refused", known: true}
		s := NewStub(st, sec, NewEmitter(), eng)
		addAccountTo(t, s)

		list, err := s.ListAccounts(ctx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, "error", list[0].Status)
		require.Equal(t, "dial tcp: connection refused", list[0].Detail)
	})

	t.Run("no worker has reported yet: connecting, never ok", func(t *testing.T) {
		st, sec := teststore.Open(t)
		eng := &spyEngine{worker: &spyWorker{}, known: false}
		s := NewStub(st, sec, NewEmitter(), eng)
		addAccountTo(t, s)

		list, err := s.ListAccounts(ctx)
		require.NoError(t, err)
		require.Equal(t, "connecting", list[0].Status)
		require.Empty(t, list[0].Detail)
	})

	t.Run("worker is healthy", func(t *testing.T) {
		st, sec := teststore.Open(t)
		eng := &spyEngine{worker: &spyWorker{}, status: "ok", known: true}
		s := NewStub(st, sec, NewEmitter(), eng)
		addAccountTo(t, s)

		list, err := s.ListAccounts(ctx)
		require.NoError(t, err)
		require.Equal(t, "ok", list[0].Status)
		require.Empty(t, list[0].Detail)
	})
}
