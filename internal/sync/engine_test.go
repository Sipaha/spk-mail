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
	imapwrap "github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestEngine_TwoAccountsSyncInParallel(t *testing.T) {
	mock1, err := mockimap.Start(context.Background(), "a@x", "p")
	require.NoError(t, err)
	defer mock1.Close()
	mock2, err := mockimap.Start(context.Background(), "b@x", "p")
	require.NoError(t, err)
	defer mock2.Close()

	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	defer st.Close()

	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), make([]byte, 32))
	require.NoError(t, err)

	add := func(email string, mock *mockimap.Server) int64 {
		host, port := imapwrap.SplitHostPort(mock.Addr())
		id, err := st.InsertAccount(context.Background(), storage.AccountRow{
			Name: email, Email: email, IMAPHost: host, IMAPPort: port, IMAPUsername: email,
			UseTLS: false, Color: "#fff", CreatedAt: 0,
		})
		require.NoError(t, err)
		require.NoError(t, sec.Set(fmt.Sprintf("account:%d", id), []byte("p")))
		u := mock.User(email)
		require.NotNil(t, u)
		raw := []byte("From: x@x\r\nSubject: t-" + email + "\r\nDate: Mon, 27 Apr 2026 10:30:00 +0000\r\nMessage-ID: <" + email + ">\r\nContent-Type: text/plain\r\n\r\nb")
		_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
		require.NoError(t, err)
		return id
	}
	_ = add("a@x", mock1)
	_ = add("b@x", mock2)

	em := api.NewEmitter()
	eng := NewEngine(st, sec, em)
	go eng.Run(context.Background())

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		threads, _ := st.ListThreadsRecent(context.Background(), 100, 0)
		if len(threads) >= 2 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("expected 2 threads")
}
