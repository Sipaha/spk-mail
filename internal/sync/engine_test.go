package sync

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/spk/spk-mail/internal/events"
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
		host, port := splitHostPortAddr(mock.Addr())
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

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	em := events.NewEmitter()
	eng := NewEngine(st, sec, em)
	go eng.Run(runCtx)

	require.Eventually(t, func() bool {
		threads, _ := st.ListThreadsRecent(context.Background(), 100, 0)
		return len(threads) >= 2
	}, 5*time.Second, 100*time.Millisecond, "expected at least 2 threads from two parallel mock servers")
}

// TestEngine_AttachmentDownloaderWiring verifies that NewEngineWithDir wires
// the attachDir field and downloaders map, and that account registration
// adds a downloader entry when attachDir is non-empty. End-to-end downloader
// behavior is covered in attachment_downloader_test.go.
//
// We deliberately do NOT call StartAccount here: that would spawn the
// supervise goroutine, which in turn busy-loops imap.Dial against the
// fixture's port=1 until ctx cancel. Calling registerDownloaderForAccountLocked
// directly exercises the wiring path without the dial-retry storm.
func TestEngine_AttachmentDownloaderWiring(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	defer st.Close()
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), make([]byte, 32))
	require.NoError(t, err)

	em := events.NewEmitter()
	attachDir := filepath.Join(dir, "attach")
	e := NewEngineWithDir(st, sec, em, attachDir)
	require.NotNil(t, e.downloaders)
	require.Equal(t, attachDir, e.attachDir)

	id, err := st.InsertAccount(context.Background(), storage.AccountRow{
		Name: "x", Email: "x@x", IMAPHost: "127.0.0.1", IMAPPort: 1, IMAPUsername: "x",
		UseTLS: false, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	e.mu.Lock()
	e.registerDownloaderForAccountLocked(ctx, id)
	_, ok := e.downloaders[id]
	e.mu.Unlock()
	require.True(t, ok, "expected downloader registered for account")

	// Plain NewEngine must NOT spawn downloaders even when register is called.
	e2 := NewEngine(st, sec, em)
	require.Equal(t, "", e2.attachDir)
	require.NotNil(t, e2.downloaders)
	e2.mu.Lock()
	e2.registerDownloaderForAccountLocked(ctx, id)
	_, ok2 := e2.downloaders[id]
	e2.mu.Unlock()
	require.False(t, ok2, "NewEngine without attachDir must not spawn downloader")
}
