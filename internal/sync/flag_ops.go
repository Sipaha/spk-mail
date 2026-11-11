package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/spk/spk-mail/internal/events"
	"github.com/spk/spk-mail/internal/flagop"
	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/storage"
)

// SubmitFlagOp wakes the outbox drainer after the API's storage transaction.
// Op coordinates describe the change for adapters; they are never replayed
// directly, because only the durable, epoch-bound current intent is safe.
// A full hint channel already guarantees a wake; no HTTP request waits on IMAP.
func (w *AccountWorker) SubmitFlagOp(ctx context.Context, op flagop.Op) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if op.AccountID != w.accountID || len(op.UIDs) == 0 {
		return fmt.Errorf("invalid flag wake-up")
	}
	select {
	case w.flagOps <- op:
	default:
	}
	return nil
}

// flushFlagIntents sends bounded bulk STOREs, one per mailbox/epoch/flag/value.
// Errors leave all unacknowledged intents durable and reconnect the primary
// session through the supervisor instead of keeping a dead connection forever.
func (w *AccountWorker) flushFlagIntents(ctx context.Context, c *imap.Client) error {
	for {
		more, err := w.flushFlagIntentBatch(ctx, c)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
}

// Yield the account sync mutex between pages, just like the body-fetch path.
// Keep SELECT/STORE/ack together so stale inbound deltas cannot race the ack.
func (w *AccountWorker) flushFlagIntentBatch(ctx context.Context, c *imap.Client) (bool, error) {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	pending, err := w.store.ListDueFlagIntents(ctx, w.accountID, 500)
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		detail, err := w.store.FlagIntentFailure(ctx, w.accountID)
		if err != nil {
			return false, err
		}
		if detail != w.flagError {
			w.flagError = detail
			if detail != "" {
				w.setStatus("error", detail)
			} else {
				w.setStatus("ok", "")
			}
		}
		return false, nil
	}
	type key struct {
		folder, epoch int64
		name, flag    string
		add           bool
	}
	groups := make(map[key][]storage.FlagIntent)
	var order []key
	for _, p := range pending {
		k := key{p.FolderID, p.UIDValidity, p.FolderName, p.Flag, p.Add}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], p)
	}
	for _, k := range order {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		ps := groups[k]
		ids := make([]int64, 0, len(ps))
		uids := make([]int64, 0, len(ps))
		for _, p := range ps {
			ids = append(ids, p.ID)
			uids = append(uids, p.UID)
		}
		state, err := c.Select(ctx, k.name)
		if err != nil {
			if imap.IsCommandRejection(err) {
				if err := w.store.DeferFlagIntents(ctx, ids, time.Now().Add(30*time.Second).Unix(), "Flag synchronization SELECT rejected: "+err.Error()); err != nil {
					return false, err
				}
				continue
			}
			return false, fmt.Errorf("flag SELECT: %w", err)
		}
		if k.epoch == 0 || state.UIDValidity != k.epoch {
			// Never apply old UIDs to a replacement mailbox. The normal folder sync
			// will remove its obsolete messages. Unknown epochs cannot be replayed.
			slog.Warn("discarding obsolete flag intents", "account_id", w.accountID, "folder_id", k.folder)
			events.Emit(w.em, "WriteError", map[string]any{"account_id": w.accountID, "folder_id": k.folder, "err": "A folder was reset on the mail server. Its earlier flag changes could not be synchronized."})
		} else if err := c.StoreFlags(ctx, uids, []string{k.flag}, k.add); err != nil {
			if imap.IsCommandRejection(err) {
				if err := w.store.DeferFlagIntents(ctx, ids, time.Now().Add(30*time.Second).Unix(), "Flag synchronization STORE rejected: "+err.Error()); err != nil {
					return false, err
				}
				continue
			}
			return false, fmt.Errorf("flag STORE: %w", err)
		}
		if err := w.store.AckFlagIntents(ctx, ids); err != nil {
			return false, err
		}
	}
	return true, nil
}
