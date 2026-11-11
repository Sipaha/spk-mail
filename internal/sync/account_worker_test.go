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

// A full wake-up channel cannot block acceptance of durable intent.
func TestAccountWorker_SubmitFlagOp_QueueFull(t *testing.T) {
	w := NewAccountWorker(1, nil, nil, nil, nil)
	op := flagop.Op{AccountID: 1, FolderID: 2, UIDs: []int64{7}}
	for i := 0; i < cap(w.flagOps); i++ {
		require.NoError(t, w.SubmitFlagOp(context.Background(), op))
	}
	done := make(chan error, 1)
	go func() { done <- w.SubmitFlagOp(context.Background(), op) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("full wake channel blocked caller")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, w.SubmitFlagOp(ctx, op), context.Canceled)
}
