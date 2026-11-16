package imap

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// lineIMAPServer is a minimal raw TCP IMAP server for reproducing wire-level
// transport faults (dead IDLE sockets, half-open stalls, tagged IDLE rejections,
// unsolicited flag-only FETCH interleaved with UID FETCH).
type lineIMAPServer struct {
	ln   net.Listener
	addr string

	mu    sync.Mutex
	conns []net.Conn
}

func startLineIMAPServer(t *testing.T, handler func(conn net.Conn, r *bufio.Reader)) *lineIMAPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &lineIMAPServer{ln: ln, addr: ln.Addr().String()}
	t.Cleanup(func() {
		_ = ln.Close()
		s.mu.Lock()
		for _, c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go func(conn net.Conn) {
				defer conn.Close()
				handler(conn, bufio.NewReader(conn))
			}(c)
		}
	}()
	return s
}

func readIMAPLine(r *bufio.Reader) (tag, rest string, ok bool) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", "", false
	}
	line = strings.TrimRight(line, "\r\n")
	parts := strings.SplitN(line, " ", 2)
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}

// Reproduces the dead-IDLE socket bug: when the remote server closes the TCP
// connection (or sends BYE and closes) during IDLE, Client.Idle must detect
// connection closure immediately and close `notifs`, rather than staying open
// until the 25m/28m timer fires.
func TestIdle_DeadSocketClosesNotifsImmediately(t *testing.T) {
	idleEntered := make(chan net.Conn, 1)
	srv := startLineIMAPServer(t, func(conn net.Conn, r *bufio.Reader) {
		_, _ = fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 IDLE] ready\r\n")
		for {
			tag, rest, ok := readIMAPLine(r)
			if !ok {
				return
			}
			upper := strings.ToUpper(rest)
			switch {
			case strings.HasPrefix(upper, "LOGIN "):
				_, _ = fmt.Fprintf(conn, "%s OK [CAPABILITY IMAP4rev1 IDLE] logged in\r\n", tag)
			case strings.HasPrefix(upper, "CAPABILITY"):
				_, _ = fmt.Fprint(conn, "* CAPABILITY IMAP4rev1 IDLE\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK CAPABILITY completed\r\n", tag)
			case strings.HasPrefix(upper, "SELECT "):
				_, _ = fmt.Fprint(conn, "* 0 EXISTS\r\n* OK [UIDVALIDITY 1] ok\r\n* OK [UIDNEXT 1] ok\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK [READ-WRITE] SELECT completed\r\n", tag)
			case upper == "IDLE":
				_, _ = fmt.Fprint(conn, "+ idling\r\n")
				select {
				case idleEntered <- conn:
				default:
				}
				time.Sleep(5 * time.Second)
				return
			default:
				_, _ = fmt.Fprintf(conn, "%s OK done\r\n", tag)
			}
		}
	})

	host, port := splitHostPort(srv.addr)
	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	notifs := make(chan IdleNotification, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := c.Idle(ctx, notifs)
	defer stop()

	conn := <-idleEntered
	_, _ = fmt.Fprint(conn, "* BYE connection closing\r\n")
	_ = conn.Close()

	select {
	case _, ok := <-notifs:
		require.False(t, ok, "notifs channel must close when IDLE socket dies")
	case <-time.After(1 * time.Second):
		t.Fatal("Client.Idle stayed blocked on dead socket instead of closing notifs")
	}
}

// Reproduces half-open hang: if the server stalls on SELECT while keeping TCP
// open, Select(ctx, ...) must honor ctx deadline instead of blocking forever.
func TestSelect_RespectsContextTimeoutOnHalfOpen(t *testing.T) {
	srv := startLineIMAPServer(t, func(conn net.Conn, r *bufio.Reader) {
		_, _ = fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 IDLE] ready\r\n")
		for {
			tag, rest, ok := readIMAPLine(r)
			if !ok {
				return
			}
			upper := strings.ToUpper(rest)
			switch {
			case strings.HasPrefix(upper, "LOGIN "):
				_, _ = fmt.Fprintf(conn, "%s OK [CAPABILITY IMAP4rev1 IDLE] logged in\r\n", tag)
			case strings.HasPrefix(upper, "CAPABILITY"):
				_, _ = fmt.Fprint(conn, "* CAPABILITY IMAP4rev1 IDLE\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK CAPABILITY completed\r\n", tag)
			case strings.HasPrefix(upper, "SELECT "):
				// Simulate a half-open socket: read the command, hold TCP open, never reply.
				time.Sleep(10 * time.Second)
				return
			default:
				_, _ = fmt.Fprintf(conn, "%s OK done\r\n", tag)
			}
		}
	})

	host, port := splitHostPort(srv.addr)
	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Select(ctx, "INBOX")
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Select ignored context deadline on stalled half-open socket")
	}
}

// Reproduces unsolicited flag-only FETCH arriving before the body FETCH for the
// same UID: FetchByUIDs must still return the full message Raw bytes instead of
// delivering an empty-body FetchedMessage.
func TestFetchByUIDs_UnsolicitedFlagFetchDoesNotDropBody(t *testing.T) {
	rawMsg := "From: Bob <bob@example.com>\r\nSubject: Unsolicited race\r\nMessage-ID: <unsol@example.test>\r\n\r\nactual body"
	var fetchCalls int
	srv := startLineIMAPServer(t, func(conn net.Conn, r *bufio.Reader) {
		_, _ = fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
		for {
			tag, rest, ok := readIMAPLine(r)
			if !ok {
				return
			}
			upper := strings.ToUpper(rest)
			switch {
			case strings.HasPrefix(upper, "LOGIN "):
				_, _ = fmt.Fprintf(conn, "%s OK [CAPABILITY IMAP4rev1] logged in\r\n", tag)
			case strings.HasPrefix(upper, "CAPABILITY"):
				_, _ = fmt.Fprint(conn, "* CAPABILITY IMAP4rev1\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK CAPABILITY completed\r\n", tag)
			case strings.HasPrefix(upper, "SELECT "):
				_, _ = fmt.Fprint(conn, "* 1 EXISTS\r\n* OK [UIDVALIDITY 1] ok\r\n* OK [UIDNEXT 8] ok\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK [READ-WRITE] SELECT completed\r\n", tag)
			case strings.HasPrefix(upper, "UID FETCH "):
				fetchCalls++
				if fetchCalls == 1 {
					// Simulate an unsolicited flag-only FETCH arriving before the body response.
					_, _ = fmt.Fprintf(conn, "* 1 FETCH (UID 7 FLAGS (\\Seen))\r\n")
					_, _ = fmt.Fprintf(conn, "%s OK UID FETCH completed\r\n", tag)
				} else {
					// Retry fetch serves the complete RFC822 body.
					_, _ = fmt.Fprintf(conn, "* 1 FETCH (UID 7 FLAGS (\\Seen) INTERNALDATE \"16-Nov-2026 07:30:00 +0000\" BODY[] {%d}\r\n%s)\r\n", len(rawMsg), rawMsg)
					_, _ = fmt.Fprintf(conn, "%s OK UID FETCH completed\r\n", tag)
				}
			default:
				_, _ = fmt.Fprintf(conn, "%s OK done\r\n", tag)
			}
		}
	})

	host, port := splitHostPort(srv.addr)
	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	msgCh, errCh := c.FetchByUIDs(context.Background(), []int64{7})
	var got []FetchedMessage
	for m := range msgCh {
		got = append(got, m)
	}
	require.NoError(t, <-errCh)
	require.Len(t, got, 1)
	require.Equal(t, int64(7), got[0].UID)
	require.Equal(t, rawMsg, string(got[0].Raw))
}

func TestIdleWithErr_RejectedIDLE(t *testing.T) {
	srv := startLineIMAPServer(t, func(conn net.Conn, r *bufio.Reader) {
		_, _ = fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 IDLE] ready\r\n")
		for {
			tag, rest, ok := readIMAPLine(r)
			if !ok {
				return
			}
			upper := strings.ToUpper(rest)
			switch {
			case strings.HasPrefix(upper, "LOGIN "):
				_, _ = fmt.Fprintf(conn, "%s OK [CAPABILITY IMAP4rev1 IDLE] logged in\r\n", tag)
			case strings.HasPrefix(upper, "SELECT "):
				_, _ = fmt.Fprint(conn, "* 0 EXISTS\r\n* OK [UIDVALIDITY 1] ok\r\n* OK [UIDNEXT 1] ok\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK [READ-WRITE] SELECT completed\r\n", tag)
			case upper == "IDLE":
				_, _ = fmt.Fprintf(conn, "%s NO IDLE disabled\r\n", tag)
			default:
				_, _ = fmt.Fprintf(conn, "%s OK done\r\n", tag)
			}
		}
	})

	host, port := splitHostPort(srv.addr)
	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	notifs := make(chan IdleNotification, 4)
	stop, err := c.IdleWithErr(context.Background(), notifs)
	require.Error(t, err)
	require.True(t, IsCommandRejection(err))
	require.Nil(t, stop)
	_, ok := <-notifs
	require.False(t, ok, "notifs must close immediately on rejected IDLE")
}

func TestIdleWithErr_StopTimeoutOnStalledDONE(t *testing.T) {
	srv := startLineIMAPServer(t, func(conn net.Conn, r *bufio.Reader) {
		_, _ = fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 IDLE] ready\r\n")
		for {
			tag, rest, ok := readIMAPLine(r)
			if !ok {
				return
			}
			upper := strings.ToUpper(rest)
			switch {
			case strings.HasPrefix(upper, "LOGIN "):
				_, _ = fmt.Fprintf(conn, "%s OK [CAPABILITY IMAP4rev1 IDLE] logged in\r\n", tag)
			case strings.HasPrefix(upper, "SELECT "):
				_, _ = fmt.Fprint(conn, "* 0 EXISTS\r\n* OK [UIDVALIDITY 1] ok\r\n* OK [UIDNEXT 1] ok\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK [READ-WRITE] SELECT completed\r\n", tag)
			case upper == "IDLE":
				_, _ = fmt.Fprint(conn, "+ idling\r\n")
				// Read DONE but never send tagged OK (half-open server stall).
				_, _, _ = readIMAPLine(r)
				time.Sleep(5 * time.Second)
				return
			default:
				_, _ = fmt.Fprintf(conn, "%s OK done\r\n", tag)
			}
		}
	})

	host, port := splitHostPort(srv.addr)
	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	c.SetCommandTimeout(150 * time.Millisecond)
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	notifs := make(chan IdleNotification, 4)
	stop, err := c.IdleWithErr(context.Background(), notifs)
	require.NoError(t, err)

	err = stop()
	require.Error(t, err)
	require.True(t, c.IsClosed(), "stalled DONE must close the broken connection")
}

func TestListFolders_NoSelectAndInboxRole(t *testing.T) {
	srv := startLineIMAPServer(t, func(conn net.Conn, r *bufio.Reader) {
		_, _ = fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
		for {
			tag, rest, ok := readIMAPLine(r)
			if !ok {
				return
			}
			upper := strings.ToUpper(rest)
			switch {
			case strings.HasPrefix(upper, "LOGIN "):
				_, _ = fmt.Fprintf(conn, "%s OK [CAPABILITY IMAP4rev1] logged in\r\n", tag)
			case strings.HasPrefix(upper, "LIST "):
				_, _ = fmt.Fprint(conn, "* LIST (\\HasChildren \\Archive) \"/\" \"INBOX\"\r\n")
				_, _ = fmt.Fprint(conn, "* LIST (\\Noselect \\HasChildren) \"/\" \"[Gmail]\"\r\n")
				_, _ = fmt.Fprint(conn, "* LIST (\\HasNoChildren \\Sent) \"/\" \"[Gmail]/Sent Mail\"\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK LIST completed\r\n", tag)
			default:
				_, _ = fmt.Fprintf(conn, "%s OK done\r\n", tag)
			}
		}
	})

	host, port := splitHostPort(srv.addr)
	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()

	folders, err := c.ListFolders(context.Background())
	require.NoError(t, err)
	require.Len(t, folders, 3)

	byName := map[string]FolderInfo{}
	for _, f := range folders {
		byName[f.Name] = f
	}
	require.Equal(t, "inbox", byName["INBOX"].Role)
	require.False(t, byName["INBOX"].NoSelect)
	require.True(t, byName["[Gmail]"].NoSelect)
	require.Equal(t, "sent", byName["[Gmail]/Sent Mail"].Role)
	require.False(t, byName["[Gmail]/Sent Mail"].NoSelect)
}

func TestFetchByUIDs_HalfOpenStallTimesOut(t *testing.T) {
	srv := startLineIMAPServer(t, func(conn net.Conn, r *bufio.Reader) {
		_, _ = fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
		for {
			tag, rest, ok := readIMAPLine(r)
			if !ok {
				return
			}
			upper := strings.ToUpper(rest)
			switch {
			case strings.HasPrefix(upper, "LOGIN "):
				_, _ = fmt.Fprintf(conn, "%s OK [CAPABILITY IMAP4rev1] logged in\r\n", tag)
			case strings.HasPrefix(upper, "SELECT "):
				_, _ = fmt.Fprint(conn, "* 1 EXISTS\r\n* OK [UIDVALIDITY 1] ok\r\n* OK [UIDNEXT 2] ok\r\n")
				_, _ = fmt.Fprintf(conn, "%s OK [READ-WRITE] SELECT completed\r\n", tag)
			case strings.HasPrefix(upper, "UID FETCH "):
				time.Sleep(5 * time.Second)
				return
			default:
				_, _ = fmt.Fprintf(conn, "%s OK done\r\n", tag)
			}
		}
	})

	host, port := splitHostPort(srv.addr)
	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	c.SetCommandTimeout(150 * time.Millisecond)
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	msgCh, errCh := c.FetchByUIDs(context.Background(), []int64{1})
	for msg := range msgCh {
		t.Fatalf("unexpected message received on stalled fetch: uid=%d", msg.UID)
	}
	err = <-errCh
	require.Error(t, err)
	require.True(t, c.IsClosed())
}
