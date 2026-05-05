package sync

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

// TestDialAccount_OpensSession verifies that DialAccount returns a
// usable IMAP client wired with the account's stored credentials.
func TestDialAccount_OpensSession(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()

	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	defer st.Close()

	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)

	host, port := splitHostPortAddr(mock.Addr())
	accID, err := st.InsertAccount(context.Background(), storage.AccountRow{
		Name: "X", Email: "alice@example.com",
		IMAPHost: host, IMAPPort: port,
		IMAPUsername: "alice@example.com", UseTLS: false,
		Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	require.NoError(t, sec.Set(fmt.Sprintf("account:%d", accID), []byte("secret")))

	c, err := DialAccount(context.Background(), st, sec, accID)
	require.NoError(t, err)
	defer c.Close()
}
