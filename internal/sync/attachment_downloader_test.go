package sync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestAttachmentDownloader_FetchesAndUpdatesRow(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()

	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	defer st.Close()

	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)

	host, port := splitHostPortAddr(mock.Addr())
	accID, err := st.InsertAccount(context.Background(), storage.AccountRow{
		Name: "X", Email: "alice@example.com",
		IMAPHost: host, IMAPPort: port,
		IMAPUsername: "alice@example.com", UseTLS: false,
		Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	require.NoError(t, sec.Set(fmt.Sprintf("account:%d", accID), []byte("secret")))

	// Append a multipart message with an attachment to mock before workers start.
	u := mock.User("alice@example.com")
	require.NotNil(t, u)
	raw := []byte("From: x@y\r\n" +
		"Subject: t\r\n" +
		"Date: Mon, 27 Apr 2026 10:30:00 +0000\r\n" +
		"Message-ID: <a@x>\r\n" +
		"MIME-Version: 1.0\r\n" +
		`Content-Type: multipart/mixed; boundary="b"` + "\r\n" +
		"\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"body\r\n" +
		"--b\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"x.bin\"\r\n" +
		"\r\n" +
		"DATA\r\n" +
		"--b--\r\n")
	_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)

	em := api.NewEmitter()
	writer := NewStoreWriter(st, em)
	go writer.Run(context.Background())

	w := NewAccountWorker(accID, st, sec, writer, em)
	go w.Run(context.Background())

	// Wait for the AccountWorker + StoreWriter to insert the message and its
	// attachment row.
	deadline := time.Now().Add(5 * time.Second)
	var attID int64
	for time.Now().Before(deadline) {
		row := st.DB().QueryRow(`SELECT id FROM attachments LIMIT 1`)
		if err := row.Scan(&attID); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotZero(t, attID, "attachment row was never inserted")

	// Drive the downloader directly via runOnce — calling Run would have us
	// wait for a 5s ticker, which races the test deadline. Same package so
	// runOnce is reachable.
	d := NewAttachmentDownloader(accID, st, sec, em, filepath.Join(dir, "attachments"))
	d.runOnce(context.Background())

	var lp *string
	require.NoError(t, st.DB().QueryRow(`SELECT local_path FROM attachments WHERE id = ?`, attID).Scan(&lp))
	require.NotNil(t, lp, "local_path should be set after download")

	body, err := os.ReadFile(*lp)
	require.NoError(t, err)
	require.Contains(t, string(body), "DATA")
}
