package imap

import (
	"bytes"
	"context"
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
	case <-time.After(2 * time.Second):
		t.Fatal("no IDLE notification within 2s")
	}
}
