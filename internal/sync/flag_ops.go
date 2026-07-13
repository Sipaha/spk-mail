package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/spk/spk-mail/internal/flagop"
)

// flagOpEnqueueTimeout is how long SubmitFlagOp blocks when the queue is
// full before returning an error to the caller.
const flagOpEnqueueTimeout = 2 * time.Second

// SubmitFlagOp queues a flag operation for async UID STORE. It blocks briefly
// when the queue is full; returns an error if the queue stays full. An empty
// UIDs slice is rejected at the boundary — the doc on flagop.Op states it
// must hold at least one UID, and accepting an empty slice would silently
// pass through to a no-op StoreFlags + a misleading "uids=[]" warning if
// the worker logged the dropped path.
//
// The only reader of the queue is the worker's runOnce loop, which is NOT
// running while the worker is down and supervise backs off (up to 300s). So a
// full queue can stay full for minutes; callers sit on the HTTP request path
// and must be able to walk away — hence ctx. Cancelling the request aborts the
// enqueue instead of pinning the handler for the full timeout.
func (w *AccountWorker) SubmitFlagOp(ctx context.Context, op flagop.Op) error {
	if len(op.UIDs) == 0 {
		slog.Warn("flag op rejected: empty UIDs",
			"account_id", w.accountID, "folder_id", op.FolderID,
			"add", op.Add, "flags", op.Flags)
		return fmt.Errorf("flag op rejected: empty UIDs")
	}
	timer := time.NewTimer(flagOpEnqueueTimeout)
	defer timer.Stop()
	select {
	case w.flagOps <- op:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("flag op dropped: %w", ctx.Err())
	case <-timer.C:
		slog.Warn("flag op dropped: queue full",
			"account_id", w.accountID,
			"folder_id", op.FolderID,
			"uids", op.UIDs,
			"add", op.Add,
			"flags", op.Flags)
		return fmt.Errorf("flag op dropped: queue full")
	}
}
