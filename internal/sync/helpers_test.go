package sync

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

// splitHostPortAddr is a test-only helper: takes "host:port" (as produced by
// mockimap.Server.Addr) and returns the parts in the storage row's typed shape.
func splitHostPortAddr(addr string) (string, int) {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return host, p
}

// mockAccountFixture holds a mock IMAP server wired to a persisted account row.
type mockAccountFixture struct {
	Mock    *mockimap.Server
	Store   *storage.Store
	Secrets *secrets.Store
	AccID   int64
	Dir     string
}

// setupMockAccount starts a mock IMAP server, opens an isolated store, and
// inserts an account row with credentials stored in secrets.
func setupMockAccount(t *testing.T, email, password string) mockAccountFixture {
	t.Helper()
	mock, err := mockimap.Start(context.Background(), email, password)
	require.NoError(t, err)
	t.Cleanup(func() { mock.Close() })

	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)

	host, port := splitHostPortAddr(mock.Addr())
	accID, err := st.InsertAccount(context.Background(), storage.AccountRow{
		Name: "X", Email: email,
		IMAPHost: host, IMAPPort: port,
		IMAPUsername: email, UseTLS: false,
		Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	require.NoError(t, sec.Set(fmt.Sprintf("account:%d", accID), []byte(password)))

	return mockAccountFixture{Mock: mock, Store: st, Secrets: sec, AccID: accID, Dir: dir}
}
