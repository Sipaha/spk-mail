package api

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	imapproto "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/spk/spk-mail/internal/events"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	mailsync "github.com/spk/spk-mail/internal/sync"
	"github.com/stretchr/testify/require"
)

// An offline read must survive a new worker, even if no worker existed when
// the API accepted it. Inspect FLAGS on a separate TCP session, not SQLite.
func TestReadSync_OfflineReadSurvivesWorkerRestart(t *testing.T) {
	ctx := context.Background()
	server, err := mockimap.Start(ctx, "reader@example.test", "fixture")
	require.NoError(t, err)
	defer server.Close()
	user := server.User("reader@example.test")
	require.NoError(t, user.Create("Other", nil))
	for _, folder := range []string{"INBOX", "Other"} {
		_, err = user.Append(folder, bytes.NewReader([]byte("From: test@example.test\r\nSubject: fixture\r\nMessage-ID: <"+folder+"@test>\r\n\r\nfixture")), &imapproto.AppendOptions{Flags: []imapproto.Flag{imapproto.FlagFlagged}})
		require.NoError(t, err)
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mail.sqlite")
	st, err := storage.Open(ctx, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), make([]byte, 32))
	require.NoError(t, err)
	host, portString, _ := net.SplitHostPort(server.Addr())
	port, _ := strconv.Atoi(portString)
	acc, err := st.InsertAccount(ctx, storage.AccountRow{Name: "Fixture", Email: "reader@example.test", IMAPHost: host, IMAPPort: port, IMAPUsername: "reader@example.test", Color: "#fff"})
	require.NoError(t, err)
	require.NoError(t, sec.Set(fmt.Sprintf("account:%d", acc), []byte("fixture")))
	em := events.NewEmitter()
	start := func() func() {
		runCtx, cancel := context.WithCancel(ctx)
		writer := mailsync.NewStoreWriter(st, em, "")
		writerDone, workerDone := make(chan struct{}), make(chan struct{})
		go func() { defer close(writerDone); writer.Run(runCtx) }()
		worker := mailsync.NewAccountWorker(acc, st, sec, writer, em)
		go func() { defer close(workerDone); worker.Run(runCtx) }()
		return func() { cancel(); <-workerDone; <-writerDone }
	}
	stop := start()
	var msgID int64
	require.Eventually(t, func() bool {
		fs, _ := st.ListFolders(ctx, acc)
		for _, f := range fs {
			if f.Name == "INBOX" {
				ts, _ := st.ListThreads(ctx, storage.ThreadFilter{FolderID: &f.ID}, 10, 0)
				if len(ts) > 0 {
					ms, _ := st.GetMessagesByThread(ctx, ts[0].ID)
					if len(ms) > 0 {
						msgID = ms[0].ID
						return true
					}
				}
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)
	stop()
	stub := NewStub(st, sec, em, nil)
	require.NoError(t, stub.MarkRead(ctx, []int64{msgID}))
	require.NoError(t, stub.MarkRead(ctx, []int64{msgID}))
	local, err := st.GetMessage(ctx, msgID)
	require.NoError(t, err)
	require.Contains(t, local.Flags, `Seen`)
	require.NoError(t, st.Close())
	st, err = storage.Open(ctx, dbPath)
	require.NoError(t, err)
	stop = start()
	defer stop()
	probe, err := imapclient.DialInsecure(server.Addr(), nil)
	require.NoError(t, err)
	defer probe.Close()
	require.NoError(t, probe.Login("reader@example.test", "fixture").Wait())
	flags := func(folder string) []imapproto.Flag {
		_, err := probe.Select(folder, nil).Wait()
		require.NoError(t, err)
		rows, err := probe.Fetch(imapproto.UIDSetNum(1), &imapproto.FetchOptions{Flags: true}).Collect()
		require.NoError(t, err)
		require.Len(t, rows, 1)
		return rows[0].Flags
	}
	require.Eventually(t, func() bool {
		for _, f := range flags("INBOX") {
			if f == imapproto.FlagSeen {
				return true
			}
		}
		return false
	}, 3*time.Second, 30*time.Millisecond, "API accepted local Seen but server never received it after restart")
	require.Contains(t, flags("INBOX"), imapproto.FlagFlagged)
	require.NotContains(t, flags("Other"), imapproto.FlagSeen, "same UID in another mailbox must remain unread")
}
