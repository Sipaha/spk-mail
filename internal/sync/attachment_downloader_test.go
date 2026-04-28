package sync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	em := api.NewEmitter()
	writer := NewStoreWriter(st, em)
	go writer.Run(runCtx)

	w := NewAccountWorker(accID, st, sec, writer, em)
	go w.Run(runCtx)

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

// TestAttachmentDownloader_RejectsPathTraversal asserts that a malicious
// Content-Disposition filename containing ".." segments cannot escape the
// attachment root. The downloader strips directory components via
// filepath.Base before joining onto rootDir.
func TestAttachmentDownloader_RejectsPathTraversal(t *testing.T) {
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

	u := mock.User("alice@example.com")
	require.NotNil(t, u)
	// Malicious filename: tries to escape the attachment root with ".."
	// segments. After filepath.Base sanitization the on-disk name should
	// be just "escape.bin" inside <attachDir>/<accID>/<msgID>/.
	raw := []byte("From: x@y\r\n" +
		"Subject: t\r\n" +
		"Date: Mon, 27 Apr 2026 10:30:00 +0000\r\n" +
		"Message-ID: <evil@x>\r\n" +
		"MIME-Version: 1.0\r\n" +
		`Content-Type: multipart/mixed; boundary="b"` + "\r\n" +
		"\r\n" +
		"--b\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"body\r\n" +
		"--b\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"../../escape.bin\"\r\n" +
		"\r\n" +
		"PWNED\r\n" +
		"--b--\r\n")
	_, err = u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	em := api.NewEmitter()
	writer := NewStoreWriter(st, em)
	go writer.Run(runCtx)

	w := NewAccountWorker(accID, st, sec, writer, em)
	go w.Run(runCtx)

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

	attachRoot := filepath.Join(dir, "attachments")
	d := NewAttachmentDownloader(accID, st, sec, em, attachRoot)
	d.runOnce(context.Background())

	var lp *string
	require.NoError(t, st.DB().QueryRow(`SELECT local_path FROM attachments WHERE id = ?`, attID).Scan(&lp))
	require.NotNil(t, lp, "local_path should be set after download")

	// Resolve both paths to absolute form before checking containment so
	// any ".." in the stored path would be detected (it shouldn't be — we
	// sanitize via filepath.Base).
	absRoot, err := filepath.Abs(attachRoot)
	require.NoError(t, err)
	absStored, err := filepath.Abs(*lp)
	require.NoError(t, err)
	rel, err := filepath.Rel(absRoot, absStored)
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(rel, ".."),
		"attachment escaped root: stored=%s root=%s rel=%s", absStored, absRoot, rel)
	// On-disk basename must be the sanitized form.
	require.Equal(t, "escape.bin", filepath.Base(*lp))

	// Belt-and-suspenders: the would-be escape target must not exist.
	escapePath := filepath.Join(dir, "escape.bin")
	_, statErr := os.Stat(escapePath)
	require.True(t, os.IsNotExist(statErr), "file escaped to %s", escapePath)
}
