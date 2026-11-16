package sync

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	stdsync "sync"
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
	"github.com/stretchr/testify/require"
)

type synthSess struct {
	imapserver.Session
	conn    *imapserver.Conn
	srv     *synthIMAPServer
	mailbox string
}

func (s *synthSess) Select(mailbox string, opts *proto.SelectOptions) (*proto.SelectData, error) {
	if s.srv.shouldStall() {
		time.Sleep(5 * time.Second)
		return nil, context.DeadlineExceeded
	}
	if s.srv.rejectINBOX.Load() && strings.EqualFold(mailbox, "INBOX") {
		return nil, &proto.Error{Type: proto.StatusResponseTypeNo, Text: "INBOX temporarily unavailable"}
	}
	if s.srv.rejectRestricted.Load() && mailbox == "Restricted" {
		return nil, &proto.Error{Type: proto.StatusResponseTypeNo, Text: "mailbox permission denied"}
	}
	s.mailbox = mailbox
	return s.Session.Select(mailbox, opts)
}

func (s *synthSess) Fetch(w *imapserver.FetchWriter, numSet proto.NumSet, opts *proto.FetchOptions) error {
	if s.mailbox == "Archive" && opts != nil && len(opts.BodySection) > 0 {
		if delay := time.Duration(s.srv.fetchDelayNs.Load()); delay > 0 {
			// Delay only Archive body fetches to simulate a slow bulk initial sync.
			time.Sleep(delay)
		}
	}
	return s.Session.Fetch(w, numSet, opts)
}

func (s *synthSess) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if s.srv.rejectIDLE.Load() {
		return &proto.Error{Type: proto.StatusResponseTypeNo, Text: "IDLE disabled"}
	}
	if s.srv.dropNextIDLE.Swap(false) {
		_ = s.conn.NetConn().Close()
		return net.ErrClosed
	}
	if s.srv.suppressIDLEUpdates.Load() {
		// Hold IDLE open quietly without pushing unilateral EXISTS updates.
		<-stop
		return nil
	}
	s.srv.mu.Lock()
	s.srv.idleConns = append(s.srv.idleConns, s.conn.NetConn())
	s.srv.mu.Unlock()
	s.srv.idleCount.Add(1)
	return s.Session.Idle(w, stop)
}

type synthIMAPServer struct {
	mem  *imapmemserver.Server
	user *imapmemserver.User
	srv  *imapserver.Server
	ln   net.Listener
	addr string

	mu        stdsync.Mutex
	idleConns []net.Conn
	idleCount atomic.Int64

	rejectINBOX         atomic.Bool
	rejectRestricted    atomic.Bool
	rejectIDLE          atomic.Bool
	dropNextIDLE        atomic.Bool
	suppressIDLEUpdates atomic.Bool
	stallSelect         atomic.Bool
	fetchDelayNs        atomic.Int64
}

func (s *synthIMAPServer) shouldStall() bool {
	return s.stallSelect.Load()
}

func (s *synthIMAPServer) closeIdleConns() {
	s.mu.Lock()
	conns := s.idleConns
	s.idleConns = nil
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func startSynthIMAPServer(t *testing.T, email, password string) *synthIMAPServer {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser(email, password)
	require.NoError(t, u.Create("INBOX", nil))
	mem.AddUser(u)

	s := &synthIMAPServer{mem: mem, user: u}
	s.srv = imapserver.New(&imapserver.Options{
		InsecureAuth: true,
		Caps:         proto.CapSet{proto.CapIMAP4rev1: {}},
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &synthSess{Session: mem.NewSession(), conn: c, srv: s}, &imapserver.GreetingData{}, nil
		},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s.ln = ln
	s.addr = ln.Addr().String()
	go func() { _ = s.srv.Serve(ln) }()
	t.Cleanup(func() { _ = s.srv.Close() })
	return s
}

func setupWorkerHarness(t *testing.T, addr, email, password string) (*storage.Store, *secrets.Store, *events.Emitter, *StoreWriter, *statusTracker, int64) {
	t.Helper()
	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), make([]byte, 32))
	require.NoError(t, err)

	host, port := splitHostPortAddr(addr)
	accID, err := st.InsertAccount(context.Background(), storage.AccountRow{
		Name: "Synthetic", Email: email,
		IMAPHost: host, IMAPPort: port, IMAPUsername: email,
		UseTLS: false, Color: "#3b82f6",
	})
	require.NoError(t, err)
	require.NoError(t, sec.Set(fmt.Sprintf("account:%d", accID), []byte(password)))

	em := events.NewEmitter()
	writer := NewStoreWriter(st, em, "")
	tracker := newStatusTracker()
	return st, sec, em, writer, tracker, accID
}

func appendSynthMsg(t *testing.T, u *imapmemserver.User, folder, from, subject, msgID, body string) uint32 {
	t.Helper()
	raw := []byte(fmt.Sprintf(
		"From: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s>\r\nContent-Type: text/plain\r\n\r\n%s",
		from, subject, time.Now().UTC().Format(time.RFC1123Z), msgID, body,
	))
	data, err := u.Append(folder, bytes.NewReader(raw), &proto.AppendOptions{Time: time.Now().UTC()})
	require.NoError(t, err)
	return uint32(data.UID)
}

func serverInboxUIDs(t *testing.T, addr, email, password string) []uint32 {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	require.NoError(t, err)
	defer c.Close()
	require.NoError(t, c.Login(email, password).Wait())
	_, err = c.Select("INBOX", nil).Wait()
	require.NoError(t, err)
	data, err := c.UIDSearch(&proto.SearchCriteria{}, nil).Wait()
	require.NoError(t, err)
	var out []uint32
	if uidSet, ok := data.All.(proto.UIDSet); ok {
		for _, r := range uidSet {
			for u := r.Start; u <= r.Stop; u++ {
				out = append(out, uint32(u))
			}
		}
	}
	return out
}

func TestSyncRecovery_DeadIDLESocketReconnectsAndReceivesNewMail(t *testing.T) {
	const email, pass = "alice@example.com", "secret"
	srv := startSynthIMAPServer(t, email, pass)
	appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Initial msg", "init-1@example.test", "hello")

	st, sec, em, writer, tracker, accID := setupWorkerHarness(t, srv.addr, email, pass)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go writer.Run(ctx)

	evCh, unsub := em.Subscribe()
	t.Cleanup(unsub)

	w := NewAccountWorker(accID, st, sec, writer, em)
	w.status = tracker
	w.idleMaxLifetime = 15 * time.Second
	w.idleCatchupInterval = 15 * time.Second
	go w.Run(ctx)

	require.Eventually(t, func() bool {
		stVal, ok := tracker.get(accID)
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		return ok && stVal.State == "ok" && len(threads) == 1 && srv.idleCount.Load() >= 1
	}, 5*time.Second, 25*time.Millisecond)

	// Kill active IDLE socket from server side, then append new unread mail.
	srv.closeIdleConns()
	uid2 := appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "After IDLE drop", "drop-2@example.test", "recovered")
	require.Contains(t, serverInboxUIDs(t, srv.addr, email, pass), uid2)

	start := time.Now()
	require.Eventually(t, func() bool {
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		return len(threads) == 2
	}, 3*time.Second, 25*time.Millisecond, "new mail after dead IDLE socket must arrive quickly, not wait 15s/25m")
	require.Less(t, time.Since(start), 3*time.Second)

	// Confirm MessageArrived fired for the newly synced message.
	var gotArrived bool
	deadline := time.After(2 * time.Second)
	for !gotArrived {
		select {
		case ev := <-evCh:
			if ev.Type == "MessageArrived" && ev.Payload["subject"] == "After IDLE drop" {
				gotArrived = true
			}
		case <-deadline:
			t.Fatal("expected MessageArrived event after dead IDLE recovery")
		}
	}
}

func TestSyncRecovery_HalfOpenStallSurfacesErrorThenRecovers(t *testing.T) {
	const email, pass = "alice@example.com", "secret"
	srv := startSynthIMAPServer(t, email, pass)
	appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Seed", "seed-1@example.test", "one")

	st, sec, em, writer, tracker, accID := setupWorkerHarness(t, srv.addr, email, pass)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go writer.Run(ctx)

	w := NewAccountWorker(accID, st, sec, writer, em)
	w.status = tracker
	w.commandTimeout = 150 * time.Millisecond
	w.idleCatchupInterval = 200 * time.Millisecond
	w.idleMaxLifetime = 20 * time.Second
	go w.Run(ctx)

	require.Eventually(t, func() bool {
		stVal, ok := tracker.get(accID)
		return ok && stVal.State == "ok" && srv.idleCount.Load() >= 1
	}, 5*time.Second, 20*time.Millisecond)

	// Force half-open stall on subsequent SELECT commands and kill current IDLE socket.
	srv.stallSelect.Store(true)
	srv.closeIdleConns()

	require.Eventually(t, func() bool {
		stVal, ok := tracker.get(accID)
		return ok && stVal.State == "error" && stVal.Detail != ""
	}, 4*time.Second, 25*time.Millisecond, "stalled half-open reconnect must NOT falsely stay Synced/ok")

	// Heal the server and append a new message.
	appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Recovered mail", "rec-2@example.test", "back online")
	srv.stallSelect.Store(false)

	require.Eventually(t, func() bool {
		stVal, ok := tracker.get(accID)
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		return ok && stVal.State == "ok" && len(threads) == 2
	}, 5*time.Second, 25*time.Millisecond)
}

func TestSyncRecovery_SilentMissedEXISTSCaughtByPeriodicCatchup(t *testing.T) {
	const email, pass = "alice@example.com", "secret"
	srv := startSynthIMAPServer(t, email, pass)
	srv.suppressIDLEUpdates.Store(true)
	appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Seed", "seed-1@example.test", "one")

	st, sec, em, writer, tracker, accID := setupWorkerHarness(t, srv.addr, email, pass)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go writer.Run(ctx)

	w := NewAccountWorker(accID, st, sec, writer, em)
	w.status = tracker
	w.idleMaxLifetime = 30 * time.Second
	w.idleCatchupInterval = 200 * time.Millisecond
	go w.Run(ctx)

	require.Eventually(t, func() bool {
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		return len(threads) == 1
	}, 3*time.Second, 20*time.Millisecond)

	// Append without IDLE EXISTS push.
	appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Silent arrival", "silent-2@example.test", "missed push")

	require.Eventually(t, func() bool {
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		return len(threads) == 2
	}, 2*time.Second, 25*time.Millisecond, "periodic in-session catchup must recover silent missed EXISTS before 30s session expiry")
}

func TestSyncRecovery_SessionExpiryRotatesAndCatchesUp(t *testing.T) {
	const email, pass = "alice@example.com", "secret"
	srv := startSynthIMAPServer(t, email, pass)
	srv.suppressIDLEUpdates.Store(true)
	appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Seed", "seed-1@example.test", "one")

	st, sec, em, writer, tracker, accID := setupWorkerHarness(t, srv.addr, email, pass)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go writer.Run(ctx)

	w := NewAccountWorker(accID, st, sec, writer, em)
	w.status = tracker
	w.idleMaxLifetime = 250 * time.Millisecond
	w.idleCatchupInterval = 10 * time.Second
	go w.Run(ctx)

	require.Eventually(t, func() bool {
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		return len(threads) == 1
	}, 3*time.Second, 20*time.Millisecond)

	appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Rotated catchup", "rot-2@example.test", "two")

	require.Eventually(t, func() bool {
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		stVal, _ := tracker.get(accID)
		return len(threads) == 2 && stVal.State == "ok"
	}, 3*time.Second, 25*time.Millisecond)
}

func TestSyncRecovery_UnsupportedAndRejectedIDLEFallsBackToPolling(t *testing.T) {
	t.Run("runtime IDLE NO rejection", func(t *testing.T) {
		const email, pass = "alice@example.com", "secret"
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close()

		var mu stdsync.Mutex
		msgs := []string{"From: Bob <bob@example.com>\r\nSubject: First\r\nMessage-ID: <1@example.test>\r\n\r\nfirst"}
		var idleCalls atomic.Int32

		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					r := bufio.NewReader(c)
					_, _ = fmt.Fprint(c, "* OK [CAPABILITY IMAP4rev1 IDLE] ready\r\n")
					for {
						line, err := r.ReadString('\n')
						if err != nil {
							return
						}
						line = strings.TrimRight(line, "\r\n")
						parts := strings.SplitN(line, " ", 2)
						tag, rest := parts[0], ""
						if len(parts) > 1 {
							rest = parts[1]
						}
						upper := strings.ToUpper(rest)
						switch {
						case strings.HasPrefix(upper, "LOGIN "):
							_, _ = fmt.Fprintf(c, "%s OK [CAPABILITY IMAP4rev1 IDLE] ok\r\n", tag)
						case strings.HasPrefix(upper, "LIST "):
							_, _ = fmt.Fprint(c, "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n")
							_, _ = fmt.Fprintf(c, "%s OK LIST ok\r\n", tag)
						case strings.HasPrefix(upper, "SELECT "):
							mu.Lock()
							n := len(msgs)
							mu.Unlock()
							_, _ = fmt.Fprintf(c, "* %d EXISTS\r\n* OK [UIDVALIDITY 1] ok\r\n* OK [UIDNEXT %d] ok\r\n", n, n+1)
							_, _ = fmt.Fprintf(c, "%s OK [READ-WRITE] SELECT ok\r\n", tag)
						case strings.HasPrefix(upper, "UID SEARCH "):
							mu.Lock()
							n := len(msgs)
							mu.Unlock()
							since := 0
							if idx := strings.Index(upper, "UID "); idx >= 0 {
								f := strings.Fields(upper[idx+4:])
								if len(f) > 0 {
									lhs := strings.SplitN(f[0], ":", 2)[0]
									since, _ = strconv.Atoi(lhs)
								}
							}
							var hits []string
							for i := since; i <= n; i++ {
								if i >= 1 {
									hits = append(hits, strconv.Itoa(i))
								}
							}
							if len(hits) > 0 {
								_, _ = fmt.Fprintf(c, "* SEARCH %s\r\n", strings.Join(hits, " "))
							} else {
								_, _ = fmt.Fprint(c, "* SEARCH\r\n")
							}
							_, _ = fmt.Fprintf(c, "%s OK SEARCH ok\r\n", tag)
						case strings.HasPrefix(upper, "UID FETCH "):
							mu.Lock()
							snapshot := append([]string(nil), msgs...)
							mu.Unlock()
							// Parse requested UIDs roughly from command.
							for idx, raw := range snapshot {
								uid := idx + 1
								if strings.Contains(rest, strconv.Itoa(uid)) {
									_, _ = fmt.Fprintf(c, "* %d FETCH (UID %d FLAGS () INTERNALDATE \"16-Nov-2026 07:30:00 +0000\" BODY[] {%d}\r\n%s)\r\n", uid, uid, len(raw), raw)
								}
							}
							_, _ = fmt.Fprintf(c, "%s OK FETCH ok\r\n", tag)
						case upper == "IDLE":
							idleCalls.Add(1)
							_, _ = fmt.Fprintf(c, "%s NO IDLE disabled\r\n", tag)
						default:
							_, _ = fmt.Fprintf(c, "%s OK ok\r\n", tag)
						}
					}
				}(conn)
			}
		}()

		st, sec, em, writer, tracker, accID := setupWorkerHarness(t, ln.Addr().String(), email, pass)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go writer.Run(ctx)

		evCh, unsub := em.Subscribe()
		t.Cleanup(unsub)

		w := NewAccountWorker(accID, st, sec, writer, em)
		w.status = tracker
		w.pollInterval = 100 * time.Millisecond
		go w.Run(ctx)

		require.Eventually(t, func() bool {
			threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
			return len(threads) == 1 && idleCalls.Load() >= 1
		}, 3*time.Second, 20*time.Millisecond)

		mu.Lock()
		msgs = append(msgs, "From: Bob <bob@example.com>\r\nSubject: Poll fallback\r\nMessage-ID: <2@example.test>\r\n\r\npolled")
		mu.Unlock()

		require.Eventually(t, func() bool {
			threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
			stVal, _ := tracker.get(accID)
			return len(threads) == 2 && stVal.State == "ok"
		}, 3*time.Second, 20*time.Millisecond)

		// Verify fallback polling of INBOX still emits MessageArrived.
		var arrived bool
		deadline := time.After(2 * time.Second)
		for !arrived {
			select {
			case ev := <-evCh:
				if ev.Type == "MessageArrived" && ev.Payload["subject"] == "Poll fallback" {
					arrived = true
				}
			case <-deadline:
				t.Fatal("expected MessageArrived from INBOX fallback poll")
			}
		}
		require.Equal(t, int32(1), idleCalls.Load(), "rejected IDLE must not spin retrying IDLE")
	})

	t.Run("server without IDLE capability polls INBOX", func(t *testing.T) {
		const email, pass = "alice@example.com", "secret"
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close()

		var mu stdsync.Mutex
		msgs := []string{"From: Bob <bob@example.com>\r\nSubject: Cap1\r\nMessage-ID: <1@example.test>\r\n\r\nfirst"}
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					r := bufio.NewReader(c)
					_, _ = fmt.Fprint(c, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
					for {
						line, err := r.ReadString('\n')
						if err != nil {
							return
						}
						line = strings.TrimRight(line, "\r\n")
						parts := strings.SplitN(line, " ", 2)
						tag, rest := parts[0], ""
						if len(parts) > 1 {
							rest = parts[1]
						}
						upper := strings.ToUpper(rest)
						switch {
						case strings.HasPrefix(upper, "LOGIN "):
							_, _ = fmt.Fprintf(c, "%s OK [CAPABILITY IMAP4rev1] ok\r\n", tag)
						case strings.HasPrefix(upper, "LIST "):
							_, _ = fmt.Fprint(c, "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n")
							_, _ = fmt.Fprintf(c, "%s OK LIST ok\r\n", tag)
						case strings.HasPrefix(upper, "SELECT "):
							mu.Lock()
							n := len(msgs)
							mu.Unlock()
							_, _ = fmt.Fprintf(c, "* %d EXISTS\r\n* OK [UIDVALIDITY 1] ok\r\n* OK [UIDNEXT %d] ok\r\n", n, n+1)
							_, _ = fmt.Fprintf(c, "%s OK [READ-WRITE] SELECT ok\r\n", tag)
						case strings.HasPrefix(upper, "UID SEARCH "):
							mu.Lock()
							n := len(msgs)
							mu.Unlock()
							since := 0
							if idx := strings.Index(upper, "UID "); idx >= 0 {
								f := strings.Fields(upper[idx+4:])
								if len(f) > 0 {
									lhs := strings.SplitN(f[0], ":", 2)[0]
									since, _ = strconv.Atoi(lhs)
								}
							}
							var hits []string
							for i := since; i <= n; i++ {
								if i >= 1 {
									hits = append(hits, strconv.Itoa(i))
								}
							}
							if len(hits) > 0 {
								_, _ = fmt.Fprintf(c, "* SEARCH %s\r\n", strings.Join(hits, " "))
							} else {
								_, _ = fmt.Fprint(c, "* SEARCH\r\n")
							}
							_, _ = fmt.Fprintf(c, "%s OK SEARCH ok\r\n", tag)
						case strings.HasPrefix(upper, "UID FETCH "):
							mu.Lock()
							snapshot := append([]string(nil), msgs...)
							mu.Unlock()
							for idx, raw := range snapshot {
								uid := idx + 1
								if strings.Contains(rest, strconv.Itoa(uid)) {
									_, _ = fmt.Fprintf(c, "* %d FETCH (UID %d FLAGS () INTERNALDATE \"16-Nov-2026 07:30:00 +0000\" BODY[] {%d}\r\n%s)\r\n", uid, uid, len(raw), raw)
								}
							}
							_, _ = fmt.Fprintf(c, "%s OK FETCH ok\r\n", tag)
						default:
							_, _ = fmt.Fprintf(c, "%s OK ok\r\n", tag)
						}
					}
				}(conn)
			}
		}()

		st, sec, em, writer, tracker, accID := setupWorkerHarness(t, ln.Addr().String(), email, pass)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go writer.Run(ctx)

		w := NewAccountWorker(accID, st, sec, writer, em)
		w.status = tracker
		w.pollInterval = 100 * time.Millisecond
		go w.Run(ctx)

		require.Eventually(t, func() bool {
			threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
			return len(threads) == 1
		}, 3*time.Second, 20*time.Millisecond)

		mu.Lock()
		msgs = append(msgs, "From: Bob <bob@example.com>\r\nSubject: Cap2\r\nMessage-ID: <2@example.test>\r\n\r\nsecond")
		mu.Unlock()

		require.Eventually(t, func() bool {
			threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
			return len(threads) == 2
		}, 3*time.Second, 20*time.Millisecond)
	})
}

func TestSyncRecovery_InboxSyncedBeforeSlowArchiveAndRejectsInboxFailure(t *testing.T) {
	t.Run("INBOX starts IDLE immediately during slow Archive sync and Restricted rejection", func(t *testing.T) {
		const email, pass = "alice@example.com", "secret"
		srv := startSynthIMAPServer(t, email, pass)
		require.NoError(t, srv.user.Create("Archive", nil))
		require.NoError(t, srv.user.Create("Restricted", nil))
		srv.rejectRestricted.Store(true)
		appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Inbox 1", "in-1@example.test", "in1")
		appendSynthMsg(t, srv.user, "Archive", "Sender <s@example.test>", "Archive 1", "ar-1@example.test", "ar1")
		appendSynthMsg(t, srv.user, "Restricted", "Sender <s@example.test>", "Secret", "re-1@example.test", "sec")
		srv.fetchDelayNs.Store(int64(1500 * time.Millisecond))

		st, sec, em, writer, tracker, accID := setupWorkerHarness(t, srv.addr, email, pass)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go writer.Run(ctx)

		w := NewAccountWorker(accID, st, sec, writer, em)
		w.status = tracker
		go w.Run(ctx)

		// INBOX must enter IDLE and complete initial sync long before Archive's 1.5s fetch finishes.
		require.Eventually(t, func() bool {
			return srv.idleCount.Load() >= 1
		}, 600*time.Millisecond, 20*time.Millisecond, "INBOX IDLE must start before slow Archive initial sync finishes")
		stVal, ok := tracker.get(accID)
		require.True(t, ok)
		require.Equal(t, "ok", stVal.State)

		appendSynthMsg(t, srv.user, "INBOX", "Sender <s@example.test>", "Live while archive syncs", "in-2@example.test", "in2")
		require.Eventually(t, func() bool {
			threads, _ := st.ListThreadsRecent(context.Background(), 20, 0)
			for _, th := range threads {
				if th.SubjectNorm == "live while archive syncs" {
					return true
				}
			}
			return false
		}, 4*time.Second, 25*time.Millisecond)
	})

	t.Run("INBOX rejection surfaces error status instead of Synced", func(t *testing.T) {
		const email, pass = "alice@example.com", "secret"
		srv := startSynthIMAPServer(t, email, pass)
		srv.rejectINBOX.Store(true)

		st, sec, em, writer, tracker, accID := setupWorkerHarness(t, srv.addr, email, pass)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go writer.Run(ctx)

		w := NewAccountWorker(accID, st, sec, writer, em)
		w.status = tracker
		err := w.runOnce(ctx)
		require.Error(t, err)
		w.setStatus("error", err.Error())
		stVal, ok := tracker.get(accID)
		require.True(t, ok)
		require.Equal(t, "error", stVal.State)
	})
}

type failOnceBundleStore struct {
	storage.Writer
	failUID  int64
	failed   atomic.Bool
	writeErr error
}

func (s *failOnceBundleStore) InsertParsedMessageBundleIfNew(ctx context.Context, b storage.MessageBundle) (int64, int64, bool, error) {
	if b.Message.UID == s.failUID && !s.failed.Swap(true) {
		return 0, 0, false, s.writeErr
	}
	return s.Writer.InsertParsedMessageBundleIfNew(ctx, b)
}

func TestSyncRecovery_MidBatchWriteFailureDoesNotSkipUID(t *testing.T) {
	cases := []struct {
		name           string
		totalMsgs      int
		failUID        int64
		initialUIDNext int64
	}{
		{name: "fail_first_uid_on_initial_sync", totalMsgs: 3, failUID: 1},
		{name: "fail_middle_uid", totalMsgs: 3, failUID: 2},
		{name: "multi_chunk_401_msgs_fail_second_chunk_uid_201", totalMsgs: 401, failUID: 201},
		{name: "inflated_legacy_checkpoint_lowered_on_mid_batch_error", totalMsgs: 3, failUID: 2, initialUIDNext: 900},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const email, pass = "alice@example.com", "secret"
			srv := startSynthIMAPServer(t, email, pass)
			for i := 1; i <= tc.totalMsgs; i++ {
				appendSynthMsg(t, srv.user, "INBOX", "A <a@example.test>",
					fmt.Sprintf("M%d", i), fmt.Sprintf("m%d@example.test", i), fmt.Sprintf("body %d", i))
			}

			st, sec, em, _, tracker, accID := setupWorkerHarness(t, srv.addr, email, pass)
			wrapStore := &failOnceBundleStore{Writer: st, failUID: tc.failUID, writeErr: errors.New("injected disk blip")}

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			writer := NewStoreWriter(wrapStore, em, "")
			go writer.Run(ctx)

			w := NewAccountWorker(accID, wrapStore, sec, writer, em)
			w.status = tracker

			c, err := imap.Dial(ctx, imap.DialOpts{
				Host:     strings.Split(srv.addr, ":")[0],
				Port:     func() int { _, p := splitHostPortAddr(srv.addr); return p }(),
				Username: email, Password: pass,
			})
			require.NoError(t, err)
			defer c.Close()

			role := "inbox"
			fid, err := st.UpsertFolder(ctx, storage.FolderRow{
				AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDNext: tc.initialUIDNext,
			})
			require.NoError(t, err)

			// First pass: failUID fails while later UIDs commit; syncFolder must fail,
			// hold/lower uid_next strictly at failUID, and leave LastSyncedAt nil.
			err = w.syncFolder(ctx, c, fid, "INBOX", "inbox", false)
			require.Error(t, err)

			folders, err := st.ListFolders(ctx, accID)
			require.NoError(t, err)
			require.Len(t, folders, 1)
			require.Equal(t, tc.failUID, folders[0].UIDNext)
			require.Nil(t, folders[0].LastSyncedAt)

			// Retry pass: failed UID must be recovered, committed higher UIDs deduplicated cleanly,
			// and only after all chunks finish does uid_next advance to totalMsgs+1 with LastSyncedAt set.
			err = w.syncFolder(ctx, c, fid, "INBOX", "inbox", false)
			require.NoError(t, err)

			folders, err = st.ListFolders(ctx, accID)
			require.NoError(t, err)
			require.Len(t, folders, 1)
			// Pass 1 lowered any inflated legacy uid_next to failUID to protect recovery;
			// after Pass 2 succeeds across all chunks, uid_next converges to server UIDNEXT.
			require.Equal(t, int64(tc.totalMsgs+1), folders[0].UIDNext)
			require.NotNil(t, folders[0].LastSyncedAt)

			var count int
			require.NoError(t, st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE folder_id = ?`, fid).Scan(&count))
			require.Equal(t, tc.totalMsgs, count, "all messages must exist with zero loss and zero duplicates")
		})
	}
}

func TestSyncRecovery_UIDValidityEmptyResetAndPollDiscovery(t *testing.T) {
	const email, pass = "alice@example.com", "secret"
	srv := startSynthIMAPServer(t, email, pass)
	appendSynthMsg(t, srv.user, "INBOX", "A <a@example.test>", "Old msg", "old@example.test", "old")

	st, sec, em, writer, tracker, accID := setupWorkerHarness(t, srv.addr, email, pass)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go writer.Run(ctx)

	evCh, unsub := em.Subscribe()
	t.Cleanup(unsub)

	w := NewAccountWorker(accID, st, sec, writer, em)
	w.status = tracker
	c, err := w.dialConfiguredClient(ctx, storage.AccountRow{
		ID: accID, IMAPHost: strings.Split(srv.addr, ":")[0],
		IMAPPort:     func() int { _, p := splitHostPortAddr(srv.addr); return p }(),
		IMAPUsername: email,
	}, []byte(pass))
	require.NoError(t, err)
	defer c.Close()

	role := "inbox"
	fid, err := st.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role})
	require.NoError(t, err)
	require.NoError(t, w.syncFolder(ctx, c, fid, "INBOX", "inbox", false))

	threads, err := st.ListThreadsRecent(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, threads, 1)

	// Recreate INBOX empty with a new UIDVALIDITY.
	require.NoError(t, srv.user.Delete("INBOX"))
	require.NoError(t, srv.user.Create("INBOX", nil))
	require.NoError(t, w.syncFolder(ctx, c, fid, "INBOX", "inbox", false))

	threads, err = st.ListThreadsRecent(ctx, 10, 0)
	require.NoError(t, err)
	require.Empty(t, threads)

	// Confirm MessageUpdated fired for the empty UIDVALIDITY reset.
	var sawUpdated bool
	deadline := time.After(time.Second)
	for !sawUpdated {
		select {
		case ev := <-evCh:
			if ev.Type == "MessageUpdated" {
				sawUpdated = true
			}
		case <-deadline:
			t.Fatal("expected MessageUpdated on empty UIDVALIDITY wipe")
		}
	}

	// Test poll-loop folder discovery for newly added custom folders.
	require.NoError(t, srv.user.Create("Projects", nil))
	appendSynthMsg(t, srv.user, "Projects", "PM <pm@example.test>", "New project", "proj-1@example.test", "kickoff")

	accRow, err := st.GetAccount(ctx, accID)
	require.NoError(t, err)
	var targets []folderTarget
	seen := map[string]bool{}
	addTarget := func(ft folderTarget) bool {
		k := strings.ToLower(ft.Name)
		if seen[k] {
			return false
		}
		seen[k] = true
		targets = append(targets, ft)
		return true
	}
	w.runPollTick(ctx, accRow, &targets, addTarget, true)
	require.Contains(t, targets, folderTarget{Name: "Projects", Role: ""})
	threads, err = st.ListThreadsRecent(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Equal(t, "new project", threads[0].SubjectNorm)
}
