package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	stdsync "sync"
	"time"

	"github.com/spk/spk-mail/internal/events"
	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/storage"
)

// fetchBatchSize bounds each UID FETCH range. 200 is a conservative balance:
// small enough that a single FETCH stays under server-side per-command
// timeouts even when the batch lands on messages with multi-MB attachments,
// large enough that a 100k-message mailbox completes in ~500 round-trips.
const fetchBatchSize int64 = 200

// syncFolder fetches new messages from the given folder and persists them.
// When notify=true, MessageArrived events are emitted for newly-fetched unread
// inbox messages — this is the real-time-arrival path, called from runIDLE
// after a server-pushed EXISTS notification. When notify=false, fetched
// messages are stored silently — used for initial bulk catch-up at startup
// and for polling-based discovery (where multiple unread messages may be
// pulled at once and we don't want to spam N system notifications).
//
// syncMu is taken per-batch (not function-wide) so a long bulk catch-up
// against a 90k-message mailbox can yield to an IDLE-driven inbox sync
// between batches, instead of pinning that other-folder sync for minutes.
// Each batch is one UID FETCH range + one Submit drain + one UpsertFolder
// checkpoint, so per-batch granularity preserves the "one bulk fetch in
// flight per account" property the lock was originally there for.
func (w *AccountWorker) syncFolder(ctx context.Context, c *imap.Client, folderID int64, name, role string, notify bool) error {
	state, err := c.Select(ctx, name)
	if err != nil {
		slog.Warn("syncFolder Select failed", "account_id", w.accountID, "folder", name, "err", err)
		return err
	}

	stored, _ := w.store.ListFolders(ctx, w.accountID)
	var prev storage.FolderRow
	for _, s := range stored {
		if s.ID == folderID {
			prev = s
			break
		}
	}
	if prev.UIDValidity != 0 && prev.UIDValidity != state.UIDValidity {
		// UIDVALIDITY changed: nuke the folder's messages and resync.
		if err := w.store.DeleteMessagesByFolder(ctx, folderID); err != nil {
			return err
		}
		prev.UIDNext = 0
	}

	// Resume from MAX(uid) actually present in messages, not just the
	// persisted folders.uid_next. Older sync paths could insert messages
	// without ever reaching the final UpsertFolder (e.g. partial bulk sync
	// against a 90k mailbox where the connection died mid-stream), leaving
	// uid_next=0 even though messages 1..N are already stored. Without this
	// check, every restart would re-fetch those N messages from the server
	// and hit UNIQUE(account_id, folder_id, uid) violations on insert.
	dbMaxUID, err := w.store.MaxUIDByFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if dbMaxUID > prev.UIDNext {
		prev.UIDNext = dbMaxUID
	}

	// Batch fetch in chunks of fetchBatchSize UIDs. A single open-ended
	// "UID FETCH N:*" works for small mailboxes but breaks on huge ones
	// (>~3000 messages we observed Yandex closing the connection mid-stream).
	// Drive new-message fetch from a UID SEARCH against the actual max
	// UID we have in the local DB — NOT from server-reported UIDNEXT.
	//
	// Why: Yandex (and presumably any RFC-strict server) pushes EXISTS
	// for every state change of the mailbox, not just for live new
	// messages. UIDNEXT can advance on EXPUNGE, on internal index
	// bumps, on UIDs reserved but never assigned. A naive
	// `UID FETCH prev:UIDNEXT` then returns zero rows and a cursor
	// advanced past nothing useful. SEARCH gives us the actual live
	// UIDs the server has, which we can then FETCH explicitly.
	//
	// dbMaxUID is the source of truth for "what we have". The folder
	// row's UIDNext field is a checkpoint that may run one ahead of
	// dbMaxUID after a sync hit a reserved-but-empty UID; anchoring
	// SEARCH on dbMaxUID re-checks those edge UIDs on the next pass.
	rolePtr := prev.Role
	serverUIDNext := int64(state.UIDNext)
	if dbMaxUID > prev.UIDNext {
		// Recovery for the legacy "checkpoint ran ahead" case.
		prev.UIDNext = dbMaxUID
	}
	liveUIDs, err := c.UIDsAbove(ctx, dbMaxUID)
	if err != nil {
		return fmt.Errorf("UID SEARCH (account_id=%d folder=%s): %w", w.accountID, name, err)
	}

	// SEARCH empty: nothing live above our DB max. Checkpoint
	// UIDNext, emit SyncProgress, run the flag-delta sweep, return.
	// EXISTS pushed for an EXPUNGE / state-change no longer triggers
	// a wasted UID FETCH round-trip.
	if len(liveUIDs) == 0 {
		now := time.Now().Unix()
		if _, err := w.store.UpsertFolder(ctx, storage.FolderRow{
			AccountID: w.accountID, Name: name, Delimiter: prev.Delimiter, Role: rolePtr,
			UIDValidity: state.UIDValidity, UIDNext: serverUIDNext, LastSyncedAt: &now,
		}); err != nil {
			return err
		}
		if w.em != nil {
			w.em.Emit(events.Event{Type: "SyncProgress", Payload: map[string]any{
				"account_id": w.accountID, "folder_id": folderID, "folder": name,
				"done": serverUIDNext, "total": serverUIDNext,
			}})
		}
		if _, err := w.syncFlagDeltas(ctx, c, folderID, state.HighestModSeq); err != nil {
			slog.Warn("flag delta sync failed", "account_id", w.accountID, "folder", name, "err", err)
		}
		return nil
	}

	// Fetch the live UIDs in chunks of fetchBatchSize. Each chunk is
	// one UID FETCH command; the per-chunk syncMu lock preserves the
	// "one bulk fetch in flight per account" invariant.
	maxUID := dbMaxUID
	for i := 0; i < len(liveUIDs); i += int(fetchBatchSize) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		end := i + int(fetchBatchSize)
		if end > len(liveUIDs) {
			end = len(liveUIDs)
		}
		chunk := liveUIDs[i:end]
		err := func() error {
			w.syncMu.Lock()
			defer w.syncMu.Unlock()
			msgCh, errCh := c.FetchByUIDs(ctx, chunk)
			var batchAck stdsync.WaitGroup
			for msg := range msgCh {
				if msg.UID > maxUID {
					maxUID = msg.UID
				}
				batchAck.Add(1)
				if err := w.writer.Submit(ctx, IncomingMessage{
					AccountID: w.accountID, FolderID: folderID, FolderRole: role, UID: msg.UID,
					Flags: msg.Flags, InternalAt: time.Unix(msg.Internal, 0), Raw: msg.Raw,
					// IsResync gates the MessageArrived event in
					// StoreWriter: true means "stored silently".
					// Driven by syncFolder's notify flag.
					IsResync: !notify,
					Ack:      batchAck.Done,
				}); err != nil {
					batchAck.Done()
					return err
				}
			}
			if err := <-errCh; err != nil {
				return err
			}
			drained := make(chan struct{})
			go func() { batchAck.Wait(); close(drained) }()
			select {
			case <-drained:
			case <-ctx.Done():
				return ctx.Err()
			}
			// Checkpoint to max(maxUID+1, serverUIDNext): record the
			// highest UID we actually saw so a subsequent EXISTS
			// doesn't replay these, but never regress below what the
			// server already announced.
			ckpt := maxUID + 1
			if ckpt < serverUIDNext {
				ckpt = serverUIDNext
			}
			now := time.Now().Unix()
			if _, err := w.store.UpsertFolder(ctx, storage.FolderRow{
				AccountID: w.accountID, Name: name, Delimiter: prev.Delimiter, Role: rolePtr,
				UIDValidity: state.UIDValidity, UIDNext: ckpt, LastSyncedAt: &now,
			}); err != nil {
				return err
			}
			if w.em != nil {
				w.em.Emit(events.Event{Type: "SyncProgress", Payload: map[string]any{
					"account_id": w.accountID, "folder_id": folderID, "folder": name,
					"done": int64(end), "total": int64(len(liveUIDs)),
				}})
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	// CONDSTORE flag-delta sweep at the end of every successful
	// syncFolder pass — initial bulk, IDLE post-EXISTS, runPoll all
	// funnel through here. No-op when the server doesn't support
	// CONDSTORE (e.g. Yandex) or when the watermark is current.
	if _, err := w.syncFlagDeltas(ctx, c, folderID, state.HighestModSeq); err != nil {
		slog.Warn("flag delta sync failed", "account_id", w.accountID, "folder", name, "err", err)
	}
	return nil
}

// folderIDByName resolves a folder name to its DB id for the given
// account. Used by IDLE / poll paths that hold the folder NAME (the
// IMAP address) but need the DB id for storage calls. Returns
// (0, false) when the folder isn't known yet — callers should skip
// the operation rather than fail.
func (w *AccountWorker) folderIDByName(ctx context.Context, accountID int64, name string) (int64, bool) {
	folders, err := w.store.ListFolders(ctx, accountID)
	if err != nil {
		return 0, false
	}
	for _, f := range folders {
		if strings.EqualFold(f.Name, name) {
			return f.ID, true
		}
	}
	return 0, false
}

// syncFlagDeltas applies CONDSTORE (RFC 7162) flag deltas for the
// currently-selected folder. The caller passes the SELECT response's
// HIGHESTMODSEQ; we compare it to the value persisted from the last
// sync of this folder and ask the server for "every message that
// changed since that watermark":
//
//	UID FETCH 1:* (FLAGS UID) (CHANGEDSINCE <stored_modseq>)
//
// The server filters in O(changed messages), not O(mailbox size).
// On a 90k-inbox where the user marked three messages read on the
// phone, this returns three rows — not a probabilistic sample of
// "the last N UIDs and pray it covered the changes".
//
// First-call semantics: when the stored watermark is 0 (just-
// migrated v8 row, or folder seen for the first time), we skip the
// sweep entirely and just record the current modseq as the baseline
// for next time. Trying to fetch CHANGEDSINCE 0 on a huge mailbox
// would mean "send me every message" — a worse brute-force than what
// we just removed.
//
// Server without CONDSTORE: serverModSeq == 0. Skip silently —
// flag-delta sync is unsupported on this server, only new-message
// flags propagate (via the body-fetch path in syncFolder).
//
// Returns the number of rows whose flags actually changed.
func (w *AccountWorker) syncFlagDeltas(ctx context.Context, c *imap.Client, folderID int64, serverModSeq uint64) (int, error) {
	if serverModSeq == 0 {
		return 0, nil // server doesn't support CONDSTORE
	}
	folders, err := w.store.ListFolders(ctx, w.accountID)
	if err != nil {
		return 0, err
	}
	var stored uint64
	for _, f := range folders {
		if f.ID == folderID {
			stored = f.HighestModSeq
			break
		}
	}
	if stored == 0 {
		// First sighting — record baseline, skip the sweep. Future
		// connects will see stored != 0 and fetch real deltas.
		return 0, w.store.SetFolderHighestModSeq(ctx, folderID, serverModSeq)
	}
	if stored >= serverModSeq {
		// Nothing changed since we last looked. The most common case
		// during normal operation — answers in zero round-trips.
		return 0, nil
	}

	msgCh, errCh := c.FetchFlagsChangedSince(ctx, stored)
	threadSet := make(map[int64]struct{})
	changed := 0
	for fd := range msgCh {
		flagsJSON, err := json.Marshal(fd.Flags)
		if err != nil {
			continue
		}
		_, threadID, didChange, err := w.store.UpdateFlagsByUID(ctx, folderID, fd.UID, string(flagsJSON))
		if err != nil {
			// Likely the row isn't in our DB yet — server reports a
			// modseq change for a UID we haven't fetched the body
			// of. The bulk-sync path will pick it up on its own
			// pass; nothing for us to do here.
			continue
		}
		if didChange {
			changed++
			if threadID != nil {
				threadSet[*threadID] = struct{}{}
			}
		}
	}
	if err := <-errCh; err != nil {
		return changed, err
	}
	for tid := range threadSet {
		if err := w.store.UpdateThreadStats(ctx, tid); err != nil {
			slog.Warn("flag delta: thread stats recompute failed", "thread_id", tid, "err", err)
		}
	}
	// Persist the new watermark so the next call asks for deltas
	// from this point. SetFolderHighestModSeq only writes if the
	// new value is greater (defensive against out-of-order calls).
	if err := w.store.SetFolderHighestModSeq(ctx, folderID, serverModSeq); err != nil {
		slog.Warn("flag delta: watermark update failed", "folder_id", folderID, "err", err)
	}
	if changed > 0 && w.em != nil {
		w.em.Emit(events.Event{Type: "MessageUpdated", Payload: map[string]any{
			"account_id": w.accountID,
			"folder_id":  folderID,
		}})
	}
	return changed, nil
}
