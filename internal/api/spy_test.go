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
	listAttsCalls atomic.Int64
	markReadCalls atomic.Int64
}

func (c *countingStore) ListAttachmentsByMessages(ctx context.Context, ids []int64) (map[int64][]storage.AttachmentRow, error) {
	c.listAttsCalls.Add(1)
	return c.Writer.ListAttachmentsByMessages(ctx, ids)
}

func (c *countingStore) MarkMessagesRead(ctx context.Context, ids []int64) (storage.MarkReadOutcome, error) {
	c.markReadCalls.Add(1)
	return c.Writer.MarkMessagesRead(ctx, ids)
}

// spyEngine + spyWorker capture IMAP STORE submissions so MarkRead's
// engine-side fan-out can be asserted in unit tests.
type spyWorker struct {
	ops []flagop.Op
}

func (w *spyWorker) SubmitFlagOp(op flagop.Op) {
	w.ops = append(w.ops, op)
}

type spyEngine struct {
	worker *spyWorker
}

func (e *spyEngine) StartAccount(ctx context.Context, id int64) {}
func (e *spyEngine) StopAccount(id int64)                       {}
func (e *spyEngine) WorkerFor(id int64) FlagOpSubmitter         { return e.worker }
