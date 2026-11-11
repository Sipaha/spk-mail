//go:build wails

package transport

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	proto "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/events"
	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
	mailsync "github.com/spk/spk-mail/internal/sync"
	"github.com/spk/spk-mail/internal/teststore"
	"github.com/stretchr/testify/require"
)

// Exercise the actual native binding wrapper without launching an operator
// desktop. It shares the durable read path with HTTP, verified separately in UI.
func TestWailsMarkRead_ReachesIMAP(t *testing.T) {
	ctx := context.Background()
	server, err := mockimap.Start(ctx, "native@test", "fixture")
	require.NoError(t, err)
	defer server.Close()
	_, err = server.User("native@test").Append("INBOX", bytes.NewReader([]byte("Subject: native fixture\r\n\r\nfixture")), &proto.AppendOptions{})
	require.NoError(t, err)
	st, sec := teststore.Open(t)
	em := events.NewEmitter()
	stub := api.NewStub(st, sec, em, nil)
	host, p, _ := net.SplitHostPort(server.Addr())
	port, _ := strconv.Atoi(p)
	acc, err := stub.AddAccount(ctx, api.AddAccountRequest{Name: "Fixture", Email: "native@test", IMAPHost: host, IMAPPort: port, IMAPUsername: "native@test", IMAPPassword: "fixture", UseTLS: false, Color: "#fff"})
	require.NoError(t, err)
	c, err := imap.Dial(ctx, imap.DialOpts{Host: host, Port: port, Username: "native@test", Password: "fixture"})
	require.NoError(t, err)
	state, err := c.Select(ctx, "INBOX")
	require.NoError(t, err)
	c.Close()
	folder, err := st.UpsertFolder(ctx, storage.FolderRow{AccountID: acc.ID, Name: "INBOX", UIDValidity: state.UIDValidity})
	require.NoError(t, err)
	msg, err := st.InsertMessage(ctx, storage.MessageRow{AccountID: acc.ID, FolderID: folder, UID: 1, Flags: `[]`})
	require.NoError(t, err)
	require.NoError(t, NewAPI(stub).MarkRead([]int64{msg}))
	runCtx, cancel := context.WithCancel(ctx)
	workerDone, writerDone := make(chan struct{}), make(chan struct{})
	writer := mailsync.NewStoreWriter(st, em, "")
	go func() { defer close(writerDone); writer.Run(runCtx) }()
	worker := mailsync.NewAccountWorker(acc.ID, st, sec, writer, em)
	go func() { defer close(workerDone); worker.Run(runCtx) }()
	defer func() { cancel(); <-workerDone; <-writerDone }()
	probe, err := imapclient.DialInsecure(server.Addr(), nil)
	require.NoError(t, err)
	defer probe.Close()
	require.NoError(t, probe.Login("native@test", "fixture").Wait())
	require.Eventually(t, func() bool {
		_, err := probe.Select("INBOX", nil).Wait()
		if err != nil {
			return false
		}
		rows, err := probe.Fetch(proto.UIDSetNum(1), &proto.FetchOptions{Flags: true}).Collect()
		if err != nil || len(rows) != 1 {
			return false
		}
		for _, f := range rows[0].Flags {
			if f == proto.FlagSeen {
				return true
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond)
}
