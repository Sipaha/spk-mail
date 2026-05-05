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

// TestAttachmentDownloader_FetchesAndUpdatesRow drives the post-v7 happy
// path: the downloader fetches the part bytes, writes them to the
// content-addressed store at <dataDir>/blobs/aa/bb/<sha>, points the
// row at the resulting blob, and the file on disk contains the IMAP
// payload byte-for-byte.
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
	writer := NewStoreWriter(st, em, "")
	go writer.Run(runCtx)

	w := NewAccountWorker(accID, st, sec, writer, em)
	go w.Run(runCtx)

	var attID int64
	require.Eventually(t, func() bool {
		row := st.DB().QueryRow(`SELECT id FROM attachments LIMIT 1`)
		return row.Scan(&attID) == nil
	}, 5*time.Second, 50*time.Millisecond, "attachment row was never inserted")
	require.NotZero(t, attID)

	d := NewAttachmentDownloader(accID, st, sec, em, dir)
	d.runOnce(context.Background())

	// Row must now point at a blob; sha resolves into a path under
	// <dir>/blobs/ that contains the IMAP body.
	blobID, sha, found, err := st.GetAttachmentBlob(context.Background(), attID)
	require.NoError(t, err)
	require.True(t, found, "blob_id should be set after download")
	require.NotZero(t, blobID)
	require.Len(t, sha, 64)

	path := storage.BlobPath(dir, sha)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), "DATA")
}

// TestAttachmentDownloader_DedupesIdenticalContent: two attachments
// with byte-identical payloads must share ONE on-disk file (refcount=2)
// and the directory tree must hold exactly one blob.
func TestAttachmentDownloader_DedupesIdenticalContent(t *testing.T) {
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

	mkMsg := func(msgID string) []byte {
		return []byte("From: x@y\r\n" +
			"Subject: t " + msgID + "\r\n" +
			"Date: Mon, 27 Apr 2026 10:30:00 +0000\r\n" +
			"Message-ID: <" + msgID + "@x>\r\n" +
			"MIME-Version: 1.0\r\n" +
			`Content-Type: multipart/mixed; boundary="b"` + "\r\n" +
			"\r\n" +
			"--b\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" +
			"body\r\n" +
			"--b\r\n" +
			"Content-Type: image/png; name=\"logo.png\"\r\n" +
			"Content-Disposition: attachment; filename=\"logo.png\"\r\n" +
			"\r\n" +
			"DUPLICATEPAYLOAD\r\n" +
			"--b--\r\n")
	}
	_, err = u.Append("INBOX", bytes.NewReader(mkMsg("a")), &imap.AppendOptions{})
	require.NoError(t, err)
	_, err = u.Append("INBOX", bytes.NewReader(mkMsg("b")), &imap.AppendOptions{})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	em := api.NewEmitter()
	writer := NewStoreWriter(st, em, "")
	go writer.Run(runCtx)
	w := NewAccountWorker(accID, st, sec, writer, em)
	go w.Run(runCtx)

	require.Eventually(t, func() bool {
		var n int
		_ = st.DB().QueryRow(`SELECT COUNT(*) FROM attachments`).Scan(&n)
		return n == 2
	}, 5*time.Second, 50*time.Millisecond, "both attachments must be inserted")

	d := NewAttachmentDownloader(accID, st, sec, em, dir)
	d.runOnce(context.Background())

	// Two attachments, ONE blob, refcount=2.
	var nBlobs int
	require.NoError(t, st.DB().QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&nBlobs))
	require.Equal(t, 1, nBlobs, "identical content must collapse to a single blob row")

	var refcount int
	require.NoError(t, st.DB().QueryRow(`SELECT refcount FROM blobs LIMIT 1`).Scan(&refcount))
	require.Equal(t, 2, refcount, "two attachments referencing the same content => refcount=2")

	// Walk the on-disk tree: exactly one file under <dir>/blobs/.
	var fileCount int
	require.NoError(t, filepath.Walk(filepath.Join(dir, "blobs"), func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			fileCount++
		}
		return nil
	}))
	require.Equal(t, 1, fileCount, "blobs/ must hold exactly one file for two identical-payload attachments")
}
