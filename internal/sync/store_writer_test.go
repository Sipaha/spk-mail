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
	w := NewStoreWriter(st, em)
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

// TestStoreWriter_DuplicateInsert verifies that submitting the same
// (account_id, folder_id, uid) tuple twice does not crash the writer or
// leave a partial row. The second submit hits the UNIQUE constraint and
// the bundle's transaction rolls back; the first row remains intact.
func TestStoreWriter_DuplicateInsert(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, _ := storage.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	defer st.Close()
	accID, _ := st.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	role := "inbox"
	fID, _ := st.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	em := api.NewEmitter()
	w := NewStoreWriter(st, em)
	go w.Run(ctx)

	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: dup", "Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"Message-ID: <dup@x>", "Content-Type: text/plain", "", "hi",
	}, "\r\n")
	msg := IncomingMessage{AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 42, Flags: []string{}, InternalAt: time.Now(), Raw: []byte(raw)}
	require.NoError(t, w.Submit(ctx, msg))
	require.NoError(t, w.Submit(ctx, msg)) // duplicate UID — must not panic the writer

	require.Eventually(t, func() bool {
		var rowCount int
		_ = st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM messages WHERE folder_id=? AND uid=?`, fID, 42).Scan(&rowCount)
		return rowCount == 1
	}, 2*time.Second, 20*time.Millisecond, "duplicate UID must not create a second row")

	// A subsequent fresh submit on a different UID must still succeed —
	// the writer survived the duplicate without going into a bad state.
	raw2 := strings.Replace(raw, "<dup@x>", "<dup2@x>", 1)
	require.NoError(t, w.Submit(ctx, IncomingMessage{AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 43, Flags: []string{}, InternalAt: time.Now(), Raw: []byte(raw2)}))
	require.Eventually(t, func() bool {
		var rowCount int
		_ = st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM messages WHERE folder_id=?`, fID).Scan(&rowCount)
		return rowCount == 2
	}, 2*time.Second, 20*time.Millisecond, "writer is stuck after duplicate insert (expected 2 rows)")
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

	w := NewStoreWriter(st, em)
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
