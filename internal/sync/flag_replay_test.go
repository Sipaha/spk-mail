package sync

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	proto "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/spk/spk-mail/internal/events"
	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/spk/spk-mail/internal/teststore"
	"github.com/stretchr/testify/require"
)

type faultSession struct {
	imapserver.Session
	conn       *imapserver.Conn
	failSelect *atomic.Bool
	denyOther  *atomic.Bool
	mailbox    string
	storeHook  func(*faultSession, *imapserver.FetchWriter, proto.NumSet, *proto.StoreFlags, *proto.StoreOptions) error
}

func (s *faultSession) Select(name string, o *proto.SelectOptions) (*proto.SelectData, error) {
	if s.failSelect.Swap(false) || (name == "Other" && s.denyOther.Load()) {
		return nil, &proto.Error{Type: proto.StatusResponseTypeNo, Text: "fixture SELECT failure"}
	}
	s.mailbox = name
	return s.Session.Select(name, o)
}
func (s *faultSession) Store(w *imapserver.FetchWriter, n proto.NumSet, f *proto.StoreFlags, o *proto.StoreOptions) error {
	if s.storeHook != nil {
		return s.storeHook(s, w, n, f, o)
	}
	return s.Session.Store(w, n, f, o)
}

type replayFixture struct {
	denyOther            atomic.Bool
	hookMu               sync.Mutex
	st                   *storage.Store
	user                 *imapmemserver.User
	worker               *AccountWorker
	account, folder, msg int64
	addr                 string
	failSelect           atomic.Bool
	hook                 func(*faultSession, *imapserver.FetchWriter, proto.NumSet, *proto.StoreFlags, *proto.StoreOptions) error
}

func newReplayFixture(t *testing.T) *replayFixture {
	t.Helper()
	ctx := context.Background()
	fx := &replayFixture{}
	mem := imapmemserver.New()
	fx.user = imapmemserver.NewUser("fixture", "fixture")
	for _, name := range []string{"INBOX", "Other"} {
		require.NoError(t, fx.user.Create(name, nil))
		_, err := fx.user.Append(name, bytes.NewReader([]byte("From: fixture@test\r\nSubject: fixture\r\n\r\nfixture")), &proto.AppendOptions{Flags: []proto.Flag{proto.FlagFlagged}})
		require.NoError(t, err)
	}
	mem.AddUser(fx.user)
	srv := imapserver.New(&imapserver.Options{InsecureAuth: true, Caps: proto.CapSet{proto.CapIMAP4rev1: {}}, NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		fx.hookMu.Lock()
		hook := fx.hook
		fx.hookMu.Unlock()
		return &faultSession{Session: mem.NewSession(), conn: c, failSelect: &fx.failSelect, denyOther: &fx.denyOther, storeHook: hook}, &imapserver.GreetingData{}, nil
	}})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fx.addr = ln.Addr().String()
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	fx.st, _ = teststore.Open(t)
	host, p, _ := net.SplitHostPort(fx.addr)
	port, _ := strconv.Atoi(p)
	fx.account, err = fx.st.InsertAccount(ctx, storage.AccountRow{Name: "Fixture", Email: "fixture@test", IMAPHost: host, IMAPPort: port, IMAPUsername: "fixture", Color: "#fff"})
	require.NoError(t, err)
	c := fx.dial(t)
	state, err := c.Select(ctx, "INBOX")
	require.NoError(t, err)
	c.Close()
	fx.folder, err = fx.st.UpsertFolder(ctx, storage.FolderRow{AccountID: fx.account, Name: "INBOX", UIDValidity: state.UIDValidity})
	require.NoError(t, err)
	fx.msg, err = fx.st.InsertMessage(ctx, storage.MessageRow{AccountID: fx.account, FolderID: fx.folder, UID: 1, Flags: `["\\Flagged"]`})
	require.NoError(t, err)
	fx.worker = NewAccountWorker(fx.account, fx.st, nil, nil, nil)
	return fx
}
func (fx *replayFixture) dial(t *testing.T) *imap.Client {
	t.Helper()
	host, p, _ := net.SplitHostPort(fx.addr)
	port, _ := strconv.Atoi(p)
	c, err := imap.Dial(context.Background(), imap.DialOpts{Host: host, Port: port, Username: "fixture", Password: "fixture"})
	require.NoError(t, err)
	return c
}
func (fx *replayFixture) flags(t *testing.T, folder string) []proto.Flag {
	t.Helper()
	c, err := imapclient.DialInsecure(fx.addr, nil)
	require.NoError(t, err)
	defer c.Close()
	require.NoError(t, c.Login("fixture", "fixture").Wait())
	_, err = c.Select(folder, nil).Wait()
	require.NoError(t, err)
	ms, err := c.Fetch(proto.UIDSetNum(1), &proto.FetchOptions{Flags: true}).Collect()
	require.NoError(t, err)
	require.Len(t, ms, 1)
	return ms[0].Flags
}
func (fx *replayFixture) pending(t *testing.T) []storage.FlagIntent {
	t.Helper()
	ps, err := fx.st.ListFlagIntents(context.Background(), fx.account, 1000)
	require.NoError(t, err)
	return ps
}

func TestFlagReplay_TransportFailureKeepsIntent(t *testing.T) {
	for _, mode := range []string{"select-no", "store-no", "disconnect", "lost-ack"} {
		t.Run(mode, func(t *testing.T) {
			fx := newReplayFixture(t)
			ctx := context.Background()
			_, err := fx.st.MarkMessagesRead(ctx, []int64{fx.msg})
			require.NoError(t, err)
			var once atomic.Bool
			fx.setHook(func(s *faultSession, w *imapserver.FetchWriter, n proto.NumSet, f *proto.StoreFlags, o *proto.StoreOptions) error {
				if once.Swap(true) {
					return s.Session.Store(w, n, f, o)
				}
				switch mode {
				case "store-no":
					return &proto.Error{Type: proto.StatusResponseTypeNo, Text: "fixture STORE failure"}
				case "disconnect":
					s.conn.NetConn().Close()
					return net.ErrClosed
				case "lost-ack":
					err := s.Session.Store(w, n, f, o)
					s.conn.NetConn().Close()
					return err
				}
				return s.Session.Store(w, n, f, o)
			})
			c := fx.dial(t)
			if mode == "select-no" {
				fx.failSelect.Store(true)
			}
			if mode == "select-no" || mode == "store-no" {
				require.NoError(t, fx.worker.flushFlagIntents(ctx, c))
			} else {
				require.Error(t, fx.worker.flushFlagIntents(ctx, c))
			}
			c.Close()
			require.Len(t, fx.pending(t), 1, "uncertain or rejected STORE must stay durable")
			if mode != "lost-ack" {
				require.NotContains(t, fx.flags(t, "INBOX"), proto.FlagSeen)
			}
			ps := fx.pending(t)
			require.NoError(t, fx.st.DeferFlagIntents(ctx, []int64{ps[0].ID}, 0, ""))
			c = fx.dial(t)
			defer c.Close()
			require.NoError(t, fx.worker.flushFlagIntents(ctx, c))
			require.Empty(t, fx.pending(t))
			require.Contains(t, fx.flags(t, "INBOX"), proto.FlagSeen)
			require.Contains(t, fx.flags(t, "INBOX"), proto.FlagFlagged)
			require.NotContains(t, fx.flags(t, "Other"), proto.FlagSeen)
		})
	}
}

func TestFlagReplay_LatestIntentWinsDuringStore(t *testing.T) {
	fx := newReplayFixture(t)
	ctx := context.Background()
	_, err := fx.st.MarkMessagesRead(ctx, []int64{fx.msg})
	require.NoError(t, err)
	var once atomic.Bool
	fx.setHook(func(s *faultSession, w *imapserver.FetchWriter, n proto.NumSet, f *proto.StoreFlags, o *proto.StoreOptions) error {
		if !once.Swap(true) { // User marks unread while the old read STORE is in flight.
			require.NoError(t, fx.st.UpdateFlags(ctx, fx.msg, `["\\Flagged"]`))
		}
		return s.Session.Store(w, n, f, o)
	})
	c := fx.dial(t)
	defer c.Close()
	require.NoError(t, fx.worker.flushFlagIntents(ctx, c))
	require.Empty(t, fx.pending(t))
	require.NotContains(t, fx.flags(t, "INBOX"), proto.FlagSeen)
	require.Contains(t, fx.flags(t, "INBOX"), proto.FlagFlagged)
}

func TestFlagReplay_UIDValidityChangeDoesNotTouchReusedUID(t *testing.T) {
	fx := newReplayFixture(t)
	ctx := context.Background()
	_, err := fx.st.MarkMessagesRead(ctx, []int64{fx.msg})
	require.NoError(t, err)
	require.NoError(t, fx.user.Delete("INBOX"))
	require.NoError(t, fx.user.Create("INBOX", nil))
	_, err = fx.user.Append("INBOX", bytes.NewReader([]byte("Subject: replacement\r\n\r\nfixture")), &proto.AppendOptions{})
	require.NoError(t, err)
	var stores atomic.Int64
	fx.setHook(func(s *faultSession, w *imapserver.FetchWriter, n proto.NumSet, f *proto.StoreFlags, o *proto.StoreOptions) error {
		stores.Add(1)
		return s.Session.Store(w, n, f, o)
	})
	c := fx.dial(t)
	defer c.Close()
	require.NoError(t, fx.worker.flushFlagIntents(ctx, c))
	require.Zero(t, stores.Load())
	require.NotContains(t, fx.flags(t, "INBOX"), proto.FlagSeen)
	require.Empty(t, fx.pending(t))
}

func (fx *replayFixture) setHook(h func(*faultSession, *imapserver.FetchWriter, proto.NumSet, *proto.StoreFlags, *proto.StoreOptions) error) {
	fx.hookMu.Lock()
	defer fx.hookMu.Unlock()
	fx.hook = h
}

func TestFlagReplay_BulkReadUsesBoundedStoreCommands(t *testing.T) {
	fx := newReplayFixture(t)
	ctx := context.Background()
	for uid := int64(2); uid <= 501; uid++ {
		_, err := fx.user.Append("INBOX", bytes.NewReader([]byte("Subject: bulk\r\n\r\nfixture")), &proto.AppendOptions{})
		require.NoError(t, err)
		_, err = fx.st.InsertMessage(ctx, storage.MessageRow{AccountID: fx.account, FolderID: fx.folder, UID: uid, Flags: `[]`})
		require.NoError(t, err)
	}
	out, err := fx.st.MarkFolderMessagesRead(ctx, fx.folder)
	require.NoError(t, err)
	require.Len(t, out.Changed, 501)
	var stores atomic.Int64
	fx.setHook(func(s *faultSession, w *imapserver.FetchWriter, n proto.NumSet, f *proto.StoreFlags, o *proto.StoreOptions) error {
		stores.Add(1)
		return s.Session.Store(w, n, f, o)
	})
	c := fx.dial(t)
	defer c.Close()
	require.NoError(t, fx.worker.flushFlagIntents(ctx, c))
	require.Equal(t, int64(2), stores.Load(), "500 intents per page, never one STORE per message")
	require.Empty(t, fx.pending(t))
	require.Contains(t, fx.flags(t, "INBOX"), proto.FlagSeen)
	require.NotContains(t, fx.flags(t, "Other"), proto.FlagSeen)
}

func TestFlagReplay_WorkerCancellationDuringStoreRetainsIntent(t *testing.T) {
	fx := newReplayFixture(t)
	ctx := context.Background()
	sec, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.bin"), make([]byte, 32))
	require.NoError(t, err)
	require.NoError(t, sec.Set(fmt.Sprintf("account:%d", fx.account), []byte("fixture")))
	em := events.NewEmitter()
	writer := NewStoreWriter(fx.st, em, "")
	writerCtx, stopWriter := context.WithCancel(ctx)
	writerDone := make(chan struct{})
	go func() { defer close(writerDone); writer.Run(writerCtx) }()
	defer func() { stopWriter(); <-writerDone }()
	_, err = fx.st.MarkMessagesRead(ctx, []int64{fx.msg})
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var once atomic.Bool
	fx.setHook(func(s *faultSession, w *imapserver.FetchWriter, n proto.NumSet, f *proto.StoreFlags, o *proto.StoreOptions) error {
		if !once.Swap(true) {
			close(entered)
			<-release
			return net.ErrClosed
		}
		return s.Session.Store(w, n, f, o)
	})
	worker := NewAccountWorker(fx.account, fx.st, sec, writer, em)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.runOnce(runCtx) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker never reached STORE")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled worker stayed blocked in STORE")
	}
	require.Len(t, fx.pending(t), 1)
	c := fx.dial(t)
	defer c.Close()
	require.NoError(t, fx.worker.flushFlagIntents(ctx, c))
	require.Contains(t, fx.flags(t, "INBOX"), proto.FlagSeen)
	require.Empty(t, fx.pending(t))
}

// R1: a permanently denied/deleted mailbox must not poison healthy outbox
// work. Retain its intent and retry later, without reconnecting on tagged NO.
func TestFlagReplay_RejectedMailboxDoesNotBlockHealthyMailbox(t *testing.T) {
	for _, mode := range []string{"select", "store"} {
		t.Run(mode, func(t *testing.T) {
			fx := newReplayFixture(t)
			ctx := context.Background()
			c := fx.dial(t)
			state, err := c.Select(ctx, "Other")
			require.NoError(t, err)
			c.Close()
			folder, err := fx.st.UpsertFolder(ctx, storage.FolderRow{AccountID: fx.account, Name: "Other", UIDValidity: state.UIDValidity})
			require.NoError(t, err)
			msg, err := fx.st.InsertMessage(ctx, storage.MessageRow{AccountID: fx.account, FolderID: folder, UID: 1, Flags: `[]`})
			require.NoError(t, err)
			_, err = fx.st.MarkMessagesRead(ctx, []int64{msg})
			require.NoError(t, err) // oldest first
			_, err = fx.st.MarkMessagesRead(ctx, []int64{fx.msg})
			require.NoError(t, err)
			var deny atomic.Bool
			deny.Store(true)
			fx.denyOther.Store(mode == "select")
			fx.setHook(func(s *faultSession, w *imapserver.FetchWriter, n proto.NumSet, f *proto.StoreFlags, o *proto.StoreOptions) error {
				if mode == "store" && s.mailbox == "Other" && deny.Load() {
					return &proto.Error{Type: proto.StatusResponseTypeNo, Text: "fixture permission denied"}
				}
				return s.Session.Store(w, n, f, o)
			})
			c = fx.dial(t)
			defer c.Close()
			require.NoError(t, fx.worker.flushFlagIntents(ctx, c))
			require.Contains(t, fx.flags(t, "INBOX"), proto.FlagSeen)
			ps := fx.pending(t)
			require.Len(t, ps, 1)
			require.Equal(t, folder, ps[0].FolderID)
			detail, err := fx.st.FlagIntentFailure(ctx, fx.account)
			require.NoError(t, err)
			require.NotEmpty(t, detail)
			require.NoError(t, fx.worker.flushFlagIntents(ctx, c), "deferred rejection must not busy retry")
			fx.denyOther.Store(false)
			deny.Store(false)
			require.NoError(t, fx.st.DeferFlagIntents(ctx, []int64{ps[0].ID}, 0, detail)) // advance retry deadline
			require.NoError(t, fx.worker.flushFlagIntents(ctx, c))
			require.Empty(t, fx.pending(t))
			require.Contains(t, fx.flags(t, "Other"), proto.FlagSeen)
			detail, err = fx.st.FlagIntentFailure(ctx, fx.account)
			require.NoError(t, err)
			require.Empty(t, detail)
		})
	}
}
