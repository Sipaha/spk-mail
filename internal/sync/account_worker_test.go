package sync

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestAccountWorker_InitialSync(t *testing.T) {
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

	// Append a message to mock before worker starts. imapmemserver.Mailbox is
	// unexported, so we go through User.Append. The non-nil AppendOptions is
	// required because (*Mailbox).appendBytes dereferences options.Time.
	u := mock.User("alice@example.com")
	require.NotNil(t, u)
	raw := []byte("From: B <b@x>\r\nSubject: hi\r\nDate: Mon, 27 Apr 2026 10:30:00 +0000\r\nMessage-ID: <m@x>\r\nContent-Type: text/plain\r\n\r\nbody")
	_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)

	em := api.NewEmitter()
	writer := NewStoreWriter(st, em)
	go writer.Run(context.Background())

	w := NewAccountWorker(accID, st, sec, writer, em)
	go w.Run(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		if len(threads) >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout waiting for sync")
}
