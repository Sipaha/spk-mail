package imap

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/stretchr/testify/require"
)

func TestClient_LoginAndList(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()

	host, port := splitHostPort(mock.Addr())

	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret", UseTLS: false})
	require.NoError(t, err)
	defer c.Close()

	folders, err := c.ListFolders(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, folders)
	require.Contains(t, folderNames(folders), "INBOX")
}

func folderNames(fs []FolderInfo) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

func TestClient_FetchBodyPart(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()
	host, port := splitHostPort(mock.Addr())

	// Append a multipart message with an attachment part.
	u := mock.User("alice@example.com")
	require.NotNil(t, u)
	raw := []byte("From: x@y\r\nSubject: t\r\nMIME-Version: 1.0\r\n" +
		`Content-Type: multipart/mixed; boundary="b"` + "\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--b\r\nContent-Type: application/octet-stream\r\n" +
		`Content-Disposition: attachment; filename="x.bin"` + "\r\n\r\n" +
		"PAYLOAD\r\n--b--\r\n")
	_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)

	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	body, err := c.FetchBodyPart(context.Background(), 1, "2")
	require.NoError(t, err)
	require.Contains(t, string(body), "PAYLOAD")
}

// TestClient_FetchBodyPart_Base64Decoded verifies that FetchBodyPart decodes
// a base64-encoded MIME part transparently — so the bytes returned to the
// caller are the actual payload, not the on-the-wire base64 ASCII.
func TestClient_FetchBodyPart_Base64Decoded(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()
	host, port := splitHostPort(mock.Addr())

	const payload = "HELLO WORLD"
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))

	u := mock.User("alice@example.com")
	require.NotNil(t, u)
	raw := []byte("From: x@y\r\nSubject: t\r\nMIME-Version: 1.0\r\n" +
		`Content-Type: multipart/mixed; boundary="b"` + "\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--b\r\nContent-Type: application/octet-stream\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		`Content-Disposition: attachment; filename="x.bin"` + "\r\n\r\n" +
		encoded + "\r\n--b--\r\n")
	_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)

	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	body, err := c.FetchBodyPart(context.Background(), 1, "2")
	require.NoError(t, err)
	// Must be the decoded payload, NOT the base64 text.
	require.Equal(t, payload, string(body))
	require.NotContains(t, string(body), encoded)
}

func TestIdle_FiresOnNewMessage(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()
	host, port := splitHostPort(mock.Addr())

	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	notifs := make(chan IdleNotification, 4)
	stop := c.Idle(context.Background(), notifs)
	defer stop()

	// Append a message via the in-memory user — fires EXISTS over the wire
	// to the IDLE-listening client, which the unilateral-data handler
	// translates into an IdleNotification on the channel above.
	u := mock.User("alice@example.com")
	require.NotNil(t, u)
	// imapmemserver.(*Mailbox).appendBytes dereferences options.Time
	// unconditionally, so an empty (non-nil) AppendOptions is required.
	raw := []byte("From: x@y\r\nSubject: t\r\n\r\nbody")
	_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)

	select {
	case n := <-notifs:
		require.Equal(t, NotifExists, n.Kind)
	case <-time.After(10 * time.Second):
		// Generous: the notification arrives in microseconds when it works;
		// the bound only has to survive scheduler starvation on a loaded box.
		t.Fatal("no IDLE notification within 10s")
	}
}

// TestIdle_ReplaysNotificationArrivedWhileNotIdling guards the DONE→FETCH→IDLE
// window. RFC 2177 forbids a FETCH while IDLE is running, so account_worker
// stops IDLE, syncs the folder on the same connection, and re-arms IDLE. A
// message landing inside that window is pushed by the server exactly once, with
// nobody listening — INBOX has no polling backstop, so losing it would hide the
// mail until the session bounces (25 min). The client must remember the missed
// push and replay it on the next Idle().
func TestIdle_ReplaysNotificationArrivedWhileNotIdling(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()
	host, port := splitHostPort(mock.Addr())

	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Select(context.Background(), "INBOX")
	require.NoError(t, err)

	u := mock.User("alice@example.com")
	require.NotNil(t, u)
	raw := []byte("From: x@y\r\nSubject: t\r\n\r\nbody")

	// First IDLE session: arm, take the EXISTS, then DONE — exactly what the
	// worker does before it fetches.
	notifs := make(chan IdleNotification, 4)
	stop := c.Idle(context.Background(), notifs)
	_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)
	select {
	case <-notifs:
	case <-time.After(2 * time.Second):
		t.Fatal("no IDLE notification within 2s")
	}
	stop()

	// The DONE→FETCH window: mail arrives with no IDLE consumer registered.
	// The server carries the EXISTS on the next command the worker issues
	// while syncing this connection (UID SEARCH / FETCH) — at which point the
	// unilateral-data handler fires with nobody listening. If the arrival
	// lands after the sync's UID SEARCH has already run, that sync will not
	// pick the message up, so the push must not be dropped.
	_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)
	_, err = c.UIDsAbove(context.Background(), 1<<30) // a command, as syncFolder would issue
	require.NoError(t, err)
	// Generous bound: this only has to outlast scheduler starvation on a loaded
	// machine, and the failure it guards (a lost push) is permanent, not slow.
	require.Eventually(t, func() bool {
		c.idleMu.Lock()
		defer c.idleMu.Unlock()
		return c.idleMissed
	}, 10*time.Second, 10*time.Millisecond, "server push during the DONE window must be recorded as missed")

	// Re-arming IDLE must replay it, so the worker re-syncs and sees the mail.
	notifs2 := make(chan IdleNotification, 4)
	stop2 := c.Idle(context.Background(), notifs2)
	defer stop2()
	select {
	case n := <-notifs2:
		require.Equal(t, NotifExists, n.Kind)
	case <-time.After(10 * time.Second):
		t.Fatal("notification that arrived while not idling was lost — the message would stay invisible until the session bounces")
	}
}
