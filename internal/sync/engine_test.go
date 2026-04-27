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

// TestEngine_AttachmentDownloaderWiring verifies that NewEngineWithDir wires
// the attachDir field and downloaders map, and that StartAccount registers a
// downloader entry when attachDir is non-empty. End-to-end downloader behavior
// is covered in attachment_downloader_test.go.
func TestEngine_AttachmentDownloaderWiring(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	defer st.Close()
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), make([]byte, 32))
	require.NoError(t, err)

	em := api.NewEmitter()
	attachDir := filepath.Join(dir, "attach")
	e := NewEngineWithDir(st, sec, em, attachDir)
	require.NotNil(t, e.downloaders)
	require.Equal(t, attachDir, e.attachDir)

	// Need a writer so AccountWorker construction doesn't panic when
	// StartAccount kicks supervise off (supervise will fail to find the
	// account in DB and bounce, but that's fine for a wiring assertion).
	e.writer = NewStoreWriter(e.store, e.em)

	id, err := st.InsertAccount(context.Background(), storage.AccountRow{
		Name: "x", Email: "x@x", IMAPHost: "127.0.0.1", IMAPPort: 1, IMAPUsername: "x",
		UseTLS: false, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.StartAccount(ctx, id)

	e.mu.Lock()
	_, ok := e.downloaders[id]
	e.mu.Unlock()
	require.True(t, ok, "expected downloader registered for account")

	// Plain NewEngine must NOT spawn downloaders.
	e2 := NewEngine(st, sec, em)
	require.Equal(t, "", e2.attachDir)
	require.NotNil(t, e2.downloaders)
}
