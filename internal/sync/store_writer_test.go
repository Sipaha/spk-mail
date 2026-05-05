package sync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/storage"
)

func TestStoreWriter_InsertsAndCreatesThread(t *testing.T) {
	st, _ := storage.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	defer st.Close()
	accID, _ := st.InsertAccount(context.Background(), storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	role := "inbox"
	fID, _ := st.UpsertFolder(context.Background(), storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	em := api.NewEmitter()
	w := NewStoreWriter(st, em, "")
	go w.Run(runCtx)

	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: Hello", "Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"Message-ID: <one@x>", "Content-Type: text/plain", "", "hi",
	}, "\r\n")
	require.NoError(t, w.Submit(runCtx, IncomingMessage{AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 1, Flags: []string{}, InternalAt: time.Now(), Raw: []byte(raw)}))

	require.Eventually(t, func() bool {
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		return len(threads) == 1
	}, 2*time.Second, 20*time.Millisecond, "expected exactly one thread after submit")
}

// TestStoreWriter_DuplicateInsert verifies that submitting two messages
// that resolve to the same (folder_id, uid) tuple but to DIFFERENT thread
// buckets does not leave an orphan thread row when the message insert
// rolls back. The first submit creates thread T1 and message M(uid=42).
// The second submit has a fresh Message-ID and a fresh subject, so
// FindThreadByMessageIDs and FindThreadBySubject both miss; the writer
// sets ExistingThreadID=0 and InsertParsedMessageBundle starts a tx
// that:
//
//   1. inserts a new thread T2 (succeeds within the tx),
//   2. tries to insert the message with UID=42 (fails on UNIQUE
//      folder_id+uid),
//   3. rolls back the entire tx — T2 must NOT survive.
//
// Without this, the rollback would leave an orphan thread with no
// messages, and the thread list would show a ghost row. Asserting on
// `threadCount == 1` after both submits is the regression sentry.
func TestStoreWriter_DuplicateInsert(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, _ := storage.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	defer st.Close()
	accID, _ := st.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	role := "inbox"
	fID, _ := st.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	em := api.NewEmitter()
	w := NewStoreWriter(st, em, "")
	go w.Run(ctx)

	mkRaw := func(msgID, subject string) []byte {
		return []byte(strings.Join([]string{
			"From: B <b@x>", "Subject: " + subject, "Date: Mon, 27 Apr 2026 10:30:00 +0000",
			"Message-ID: <" + msgID + ">", "Content-Type: text/plain", "", "hi",
		}, "\r\n"))
	}

	// First submit: creates thread T1 + message M(uid=42).
	require.NoError(t, w.Submit(ctx, IncomingMessage{
		AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 42,
		Flags: []string{}, InternalAt: time.Now(), Raw: mkRaw("orig@x", "first topic"),
	}))
	// Second submit: same (folder_id, uid), but Message-ID and Subject are
	// new so the writer cannot reuse T1. The tx will create T2, fail to
	// insert the message, and roll back — T2 must vanish with the rollback.
	require.NoError(t, w.Submit(ctx, IncomingMessage{
		AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 42,
		Flags: []string{}, InternalAt: time.Now(), Raw: mkRaw("collide@x", "second topic"),
	}))

	require.Eventually(t, func() bool {
		var rowCount int
		_ = st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM messages WHERE folder_id=? AND uid=?`, fID, 42).Scan(&rowCount)
		return rowCount == 1
	}, 2*time.Second, 20*time.Millisecond, "second submit must not create a second message row")

	// The actual rollback assertion: only the first thread survives.
	// A regression where the new-thread insert escapes the rolled-back
	// tx would push this to 2.
	var threadCount int
	require.NoError(t, st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM threads`).Scan(&threadCount))
	require.Equal(t, 1, threadCount, "rolled-back new-thread insert must NOT leave an orphan thread row")

	// Writer is still alive — a third, non-colliding submit lands cleanly.
	require.NoError(t, w.Submit(ctx, IncomingMessage{
		AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 43,
		Flags: []string{}, InternalAt: time.Now(), Raw: mkRaw("third@x", "third topic"),
	}))
	require.Eventually(t, func() bool {
		var rowCount int
		_ = st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM messages WHERE folder_id=?`, fID).Scan(&rowCount)
		return rowCount == 2
	}, 2*time.Second, 20*time.Millisecond, "writer is stuck after duplicate-uid attempt")
}

// TestStoreWriter_CapturesRawForFreshMessage: a message whose Date
// header is recent gets its raw bytes mirrored into the blob store
// and linked via raw_blob_id.
func TestStoreWriter_CapturesRawForFreshMessage(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	st, _ := storage.Open(ctx, filepath.Join(dir, "db.sqlite"))
	defer st.Close()
	accID, _ := st.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	role := "inbox"
	fID, _ := st.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	em := api.NewEmitter()
	w := NewStoreWriter(st, em, dir)
	go w.Run(ctx)

	now := time.Now().UTC().Format(time.RFC1123Z)
	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: fresh", "Date: " + now,
		"Message-ID: <fresh@x>", "Content-Type: text/plain", "", "hi",
	}, "\r\n")
	require.NoError(t, w.Submit(ctx, IncomingMessage{
		AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 1,
		Flags: []string{}, InternalAt: time.Now(), Raw: []byte(raw),
	}))

	require.Eventually(t, func() bool {
		var blobID *int64
		_ = st.DB().QueryRowContext(ctx,
			`SELECT raw_blob_id FROM messages WHERE folder_id = ? AND uid = ?`,
			fID, 1).Scan(&blobID)
		return blobID != nil
	}, 2*time.Second, 20*time.Millisecond, "raw_blob_id should be set after capture")
}

// TestStoreWriter_SkipsRawForOldMessage: a message older than the
// retention window leaves raw_blob_id NULL.
func TestStoreWriter_SkipsRawForOldMessage(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	st, _ := storage.Open(ctx, filepath.Join(dir, "db.sqlite"))
	defer st.Close()
	accID, _ := st.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	role := "inbox"
	fID, _ := st.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	em := api.NewEmitter()
	w := NewStoreWriter(st, em, dir)
	go w.Run(ctx)

	old := time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC1123Z)
	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: old", "Date: " + old,
		"Message-ID: <old@x>", "Content-Type: text/plain", "", "hi",
	}, "\r\n")
	require.NoError(t, w.Submit(ctx, IncomingMessage{
		AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 1,
		Flags: []string{}, InternalAt: time.Now(), Raw: []byte(raw),
	}))

	require.Eventually(t, func() bool {
		var n int
		_ = st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&n)
		return n == 1
	}, 2*time.Second, 20*time.Millisecond)

	var blobID *int64
	require.NoError(t, st.DB().QueryRowContext(ctx,
		`SELECT raw_blob_id FROM messages WHERE folder_id = ? AND uid = ?`, fID, 1).Scan(&blobID))
	require.Nil(t, blobID, "old message must not have raw captured")
}

// TestStoreWriter_IsResyncGatesArrived locks in the rule that MessageArrived
// only fires for live arrivals (notify=true → IsResync=false), not for the
// initial bulk catch-up. Without this gate, every cold-start would flood the
// system tray with N notifications. We assert the count exactly: a regression
// that flips the gate the wrong way would silently turn the catch-up flood
// back on, and the existing tests would still pass.
func TestStoreWriter_IsResyncGatesArrived(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	st, _ := storage.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	defer st.Close()
	accID, _ := st.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	role := "inbox"
	fID, _ := st.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	em := api.NewEmitter()
	events, unsub := em.Subscribe()
	t.Cleanup(unsub)

	w := NewStoreWriter(st, em, "")
	go w.Run(ctx)

	mkRaw := func(msgID, subject string) []byte {
		return []byte(strings.Join([]string{
			"From: B <b@x>", "Subject: " + subject, "Date: Mon, 27 Apr 2026 10:30:00 +0000",
			"Message-ID: <" + msgID + ">", "Content-Type: text/plain", "", "hi",
		}, "\r\n"))
	}

	// Resync (catch-up) — must NOT emit MessageArrived even though it's
	// inbox + unread (no \Seen). Should still emit MessageInserted.
	require.NoError(t, w.Submit(ctx, IncomingMessage{
		AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 1,
		Flags: []string{}, InternalAt: time.Now(),
		Raw:      mkRaw("resync@x", "catch-up"),
		IsResync: true,
	}))
	// Live arrival — must emit MessageArrived AND MessageInserted.
	require.NoError(t, w.Submit(ctx, IncomingMessage{
		AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 2,
		Flags: []string{}, InternalAt: time.Now(),
		Raw:      mkRaw("live@x", "live one"),
		IsResync: false,
	}))

	// Drain events until both inserts land (or timeout). Counts are exact:
	// regression that flips the gate would push arrived from 1 to 2.
	var arrived, inserted int
	deadline := time.After(3 * time.Second)
loop:
	for inserted < 2 {
		select {
		case ev, ok := <-events:
			if !ok {
				break loop
			}
			switch ev.Type {
			case "MessageInserted":
				inserted++
			case "MessageArrived":
				arrived++
			}
		case <-deadline:
			t.Fatalf("timeout waiting for inserts: inserted=%d arrived=%d", inserted, arrived)
		}
	}
	// After both inserts have been observed, give a brief window for any
	// stray late-emitted MessageArrived to surface and fail the assertion.
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
drain:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break drain
			}
			if ev.Type == "MessageArrived" {
				arrived++
			}
		case <-timer.C:
			break drain
		}
	}
	require.Equal(t, 2, inserted, "expect MessageInserted for both submissions")
	require.Equal(t, 1, arrived, "expect MessageArrived only for the live (IsResync=false) submission")
}
