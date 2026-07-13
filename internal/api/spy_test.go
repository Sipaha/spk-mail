package api

import (
	"context"
	"sync/atomic"

	"github.com/spk/spk-mail/internal/flagop"
	"github.com/spk/spk-mail/internal/storage"
)

// countingStore wraps a real storage.Writer and increments per-method counters
// so an API-layer test can assert call shape (e.g. "GetThread issues exactly
// one ListAttachmentsByMessages, not N"). Embedding the interface forwards
// every other method to the real store unchanged.
type countingStore struct {
	storage.Writer
	listAttsCalls       atomic.Int64
	markReadCalls       atomic.Int64
	markFolderReadCalls atomic.Int64
	toggleFlaggedCalls  atomic.Int64
}

func (c *countingStore) ListAttachmentsByMessages(ctx context.Context, ids []int64) (map[int64][]storage.AttachmentRow, error) {
	c.listAttsCalls.Add(1)
	return c.Writer.ListAttachmentsByMessages(ctx, ids)
}

func (c *countingStore) MarkMessagesRead(ctx context.Context, ids []int64) (storage.MarkReadOutcome, error) {
	c.markReadCalls.Add(1)
	return c.Writer.MarkMessagesRead(ctx, ids)
}

func (c *countingStore) MarkFolderMessagesRead(ctx context.Context, folderID int64) (storage.MarkReadOutcome, error) {
	c.markFolderReadCalls.Add(1)
	return c.Writer.MarkFolderMessagesRead(ctx, folderID)
}

func (c *countingStore) ToggleThreadFlagged(ctx context.Context, threadID int64) (storage.FlagToggleOutcome, error) {
	c.toggleFlaggedCalls.Add(1)
	return c.Writer.ToggleThreadFlagged(ctx, threadID)
}

// spyEngine + spyWorker capture IMAP STORE submissions so MarkRead's
// engine-side fan-out can be asserted in unit tests.
type spyWorker struct {
	ops []flagop.Op
}

func (w *spyWorker) SubmitFlagOp(_ context.Context, op flagop.Op) error {
	w.ops = append(w.ops, op)
	return nil
}

type spyEngine struct {
	worker *spyWorker
}

func (e *spyEngine) StartAccount(_ context.Context, _ int64) {}
func (e *spyEngine) StopAccount(_ int64)                     {}
func (e *spyEngine) WorkerFor(_ int64) FlagOpSubmitter       { return e.worker }
