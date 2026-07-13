package sync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mail/internal/events"
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

	em := events.NewEmitter()
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
//  1. inserts a new thread T2 (succeeds within the tx),
//  2. tries to insert the message with UID=42 (fails on UNIQUE
//     folder_id+uid),
//  3. rolls back the entire tx — T2 must NOT survive.
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

	em := events.NewEmitter()
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

	em := events.NewEmitter()
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

	em := events.NewEmitter()
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

	em := events.NewEmitter()
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

// mkRaw builds a minimal RFC822 message with a distinct Message-ID so each
// submission lands in its own thread.
func mkRaw(id int) string {
	return strings.Join([]string{
		"From: B <b@x>",
		"Subject: Drain " + string(rune('A'+id)),
		"Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"Message-ID: <drain-" + string(rune('a'+id)) + "@x>",
		"Content-Type: text/plain", "", "hi",
	}, "\r\n")
}

// TestStoreWriter_DrainOnShutdownPersistsQueuedMessages exercises Run's
// shutdown path end-to-end: messages already queued when the run ctx is
// cancelled must still land in the store (a bulk-sync batch in flight at
// shutdown would otherwise be silently lost), not dropped.
//
// All N messages are queued via Submit before Run is ever started, and the
// run ctx is cancelled before Run starts too — so Run's very first select
// iteration sees both `<-ctx.Done()` and a full `w.in` ready at once. Run
// handles this by checking ctx.Err() on the receive branch and handing any
// message it already took off the queue to drainOnShutdown as `pending`
// (which persists it on a fresh, non-cancelled context) rather than trying
// to process it with the dead outer ctx. Without that handoff — e.g. a
// regression that goes back to unconditionally calling
// w.process(ctx, m) on the receive branch — this test fails intermittently
// (verified locally: reverting the ctx.Err() check drops roughly half the
// queued messages across repeated runs, logging `WARN FindThreadBySubject
// failed ... err="context canceled"`). Run under -race -count=N to catch
// that class of regression reliably.
func TestStoreWriter_DrainOnShutdownPersistsQueuedMessages(t *testing.T) {
	ctx := context.Background()
	st, _ := storage.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	defer st.Close()
	accID, _ := st.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	role := "inbox"
	fID, _ := st.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	runCtx, cancel := context.WithCancel(ctx)
	w := NewStoreWriter(st, events.NewEmitter(), "")

	// Queue every message BEFORE Run starts consuming, then cancel: Run's
	// first iteration finds the ctx already done and the channel already
	// full.
	const queued = 3
	for i := 0; i < queued; i++ {
		require.NoError(t, w.Submit(runCtx, IncomingMessage{
			AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: int64(i + 1),
			Flags: []string{}, InternalAt: time.Now(), Raw: []byte(mkRaw(i)),
		}))
	}
	cancel()

	done := make(chan struct{})
	start := time.Now()
	go func() {
		w.Run(runCtx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(writerDrainGrace):
		t.Fatal("Run did not return within the drain grace period")
	}
	require.Less(t, time.Since(start), writerDrainGrace,
		"Run must return as soon as the queue is drained, not sit out the whole grace period")

	threads, err := st.ListThreadsRecent(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, threads, queued, "messages queued before cancel must be drained to the store, not dropped")
}

// TestStoreWriter_ShutdownIsImmediateWhenQueueEmpty is the sentry for the bug
// where drainOnShutdown blocked on a receive from a channel nobody closes:
// with an empty queue, cancelling the ctx delayed every shutdown by
// writerDrainGrace (5s).
func TestStoreWriter_ShutdownIsImmediateWhenQueueEmpty(t *testing.T) {
	ctx := context.Background()
	st, _ := storage.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	defer st.Close()

	runCtx, cancel := context.WithCancel(ctx)
	w := NewStoreWriter(st, events.NewEmitter(), "")

	done := make(chan struct{})
	go func() {
		w.Run(runCtx)
		close(done)
	}()

	// Let Run reach its select, then cancel with nothing queued.
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	cancel()

	select {
	case <-done:
	case <-time.After(writerDrainGrace):
		t.Fatal("Run blocked on an empty queue for the whole drain grace period")
	}
	require.Less(t, time.Since(start), time.Second,
		"an empty-queue shutdown must be near-instant, not bounded by writerDrainGrace")
}
