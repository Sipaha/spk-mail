package sync

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/spk/spk-mail/internal/events"
	"github.com/spk/spk-mail/internal/flagop"
	"github.com/stretchr/testify/require"
)

func TestAccountWorker_InitialSync(t *testing.T) {
	fx := setupMockAccount(t, "alice@example.com", "secret")

	// Append a message to mock before worker starts. imapmemserver.Mailbox is
	// unexported, so we go through User.Append. The non-nil AppendOptions is
	// required because (*Mailbox).appendBytes dereferences options.Time.
	u := fx.Mock.User("alice@example.com")
	require.NotNil(t, u)
	raw := []byte("From: B <b@x>\r\nSubject: hi\r\nDate: Mon, 27 Apr 2026 10:30:00 +0000\r\nMessage-ID: <m@x>\r\nContent-Type: text/plain\r\n\r\nbody")
	_, err := u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	em := events.NewEmitter()
	writer := NewStoreWriter(fx.Store, em, "")
	go writer.Run(runCtx)

	w := NewAccountWorker(fx.AccID, fx.Store, fx.Secrets, writer, em)
	go w.Run(runCtx)

	require.Eventually(t, func() bool {
		threads, _ := fx.Store.ListThreadsRecent(context.Background(), 10, 0)
		return len(threads) >= 1
	}, 3*time.Second, 50*time.Millisecond, "expected at least one thread after sync")
}

// TestAccountWorker_SubmitFlagOp_HappyPath verifies that when the flagOps
// queue has room, SubmitFlagOp returns immediately with a nil error and the
// op is actually delivered on the channel the runOnce drain loop reads from.
func TestAccountWorker_SubmitFlagOp_HappyPath(t *testing.T) {
	// SubmitFlagOp only touches w.accountID and w.flagOps, so a worker built
	// without a real store/secrets/writer/emitter is sufficient here — no
	// mock IMAP server or DB needed.
	w := NewAccountWorker(1, nil, nil, nil, nil)

	op := flagop.Op{AccountID: 1, FolderID: 2, UIDs: []int64{7}, Add: true, Flags: []string{`\Seen`}}
	require.NoError(t, w.SubmitFlagOp(context.Background(), op))

	select {
	case got := <-w.flagOps:
		require.Equal(t, op, got)
	default:
		t.Fatal("expected op to be enqueued on flagOps channel")
	}
}

// TestAccountWorker_SubmitFlagOp_QueueFull verifies the full-queue path:
// SubmitFlagOp must neither silently drop the op nor block forever. It
// should block for roughly flagOpEnqueueTimeout and then return a non-nil
// error. Filling the queue to capacity is derived from cap(w.flagOps)
// rather than a hardcoded 64 so this test survives a queue resize.
func TestAccountWorker_SubmitFlagOp_QueueFull(t *testing.T) {
	w := NewAccountWorker(1, nil, nil, nil, nil)

	capacity := cap(w.flagOps)
	for i := 0; i < capacity; i++ {
		op := flagop.Op{AccountID: 1, FolderID: 2, UIDs: []int64{int64(i + 1)}, Add: true, Flags: []string{`\Seen`}}
		require.NoError(t, w.SubmitFlagOp(context.Background(), op), "queue should accept ops up to capacity without blocking")
	}

	start := time.Now()
	err := w.SubmitFlagOp(context.Background(), flagop.Op{AccountID: 1, FolderID: 2, UIDs: []int64{999}, Add: true, Flags: []string{`\Seen`}})
	elapsed := time.Since(start)

	require.Error(t, err, "SubmitFlagOp must return an error rather than silently dropping or blocking forever when the queue is full")
	require.GreaterOrEqual(t, elapsed, flagOpEnqueueTimeout-250*time.Millisecond,
		"SubmitFlagOp returned too early — it should really wait roughly flagOpEnqueueTimeout before giving up")
	require.Less(t, elapsed, flagOpEnqueueTimeout+3*time.Second,
		"SubmitFlagOp blocked far longer than flagOpEnqueueTimeout — generous upper bound for a loaded CI machine")
}

// TestAccountWorker_SubmitFlagOp_ContextCancel verifies that a caller on the
// HTTP request path can walk away from a full queue. The queue only drains
// while the worker's runOnce loop runs, so during an IMAP outage (worker down,
// supervise backing off up to 300s) it can stay full for minutes — a cancelled
// request must abort the enqueue immediately instead of pinning the handler for
// the whole flagOpEnqueueTimeout.
func TestAccountWorker_SubmitFlagOp_ContextCancel(t *testing.T) {
	w := NewAccountWorker(1, nil, nil, nil, nil)

	for i := 0; i < cap(w.flagOps); i++ {
		op := flagop.Op{AccountID: 1, FolderID: 2, UIDs: []int64{int64(i + 1)}, Add: true, Flags: []string{`\Seen`}}
		require.NoError(t, w.SubmitFlagOp(context.Background(), op))
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := w.SubmitFlagOp(ctx, flagop.Op{AccountID: 1, FolderID: 2, UIDs: []int64{999}, Add: true, Flags: []string{`\Seen`}})
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, elapsed, flagOpEnqueueTimeout,
		"cancelling the caller's context must abort the enqueue well before the full-queue timeout")
}
