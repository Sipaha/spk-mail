package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
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
// after a server-pushed EXISTS notification or periodic catchup. When
// notify=false, fetched messages are stored silently — used for initial bulk
// catch-up at startup and for non-inbox polling.
//
// syncMu is taken per-batch (not function-wide) so a long bulk catch-up
// against a 90k-message mailbox can yield to an IDLE-driven inbox sync
// between batches, instead of pinning that other-folder sync for minutes.
func (w *AccountWorker) syncFolder(ctx context.Context, c *imap.Client, folderID int64, name, role string, notify bool) error {
	state, err := c.Select(ctx, name)
	if err != nil {
		slog.Warn("syncFolder Select failed", "account_id", w.accountID, "folder", name, "err", err)
		return err
	}

	stored, err := w.store.ListFolders(ctx, w.accountID)
	if err != nil {
		return err
	}
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
		if w.em != nil {
			w.em.Emit(events.Event{Type: "MessageUpdated", Payload: map[string]any{
				"account_id": w.accountID,
				"folder_id":  folderID,
			}})
		}
	}

	// Persist the epoch before any fetched message can become visible to the
	// UI and receive a read request. A failed partial sync must retain it too.
	if _, err := w.store.UpsertFolder(ctx, storage.FolderRow{
		AccountID: w.accountID, Name: name, Delimiter: prev.Delimiter, Role: prev.Role,
		UIDValidity: state.UIDValidity, UIDNext: prev.UIDNext, LastSyncedAt: prev.LastSyncedAt,
	}); err != nil {
		return err
	}

	// Compute the search floor from BOTH MAX(uid) in messages and the saved
	// uid_next checkpoint. Using min(dbMaxUID, prev.UIDNext-1) when prev.UIDNext > 0
	// ensures that if a mid-batch write error left a lower UID missing behind a
	// higher committed UID, the next pass re-searches from the lower checkpoint
	// instead of leaving a permanent hole.
	dbMaxUID, err := w.store.MaxUIDByFolder(ctx, folderID)
	if err != nil {
		return err
	}
	sinceUID := dbMaxUID
	if prev.UIDNext > 0 && prev.UIDNext-1 < sinceUID {
		sinceUID = prev.UIDNext - 1
	}

	rolePtr := prev.Role
	serverUIDNext := int64(state.UIDNext)

	liveUIDs, err := c.UIDsAbove(ctx, sinceUID)
	if err != nil {
		return fmt.Errorf("UID SEARCH (account_id=%d folder=%s): %w", w.accountID, name, err)
	}
	slices.Sort(liveUIDs)

	// SEARCH empty: nothing live above our checkpoint. Checkpoint
	// UIDNext, emit SyncProgress, run the flag-delta sweep, return.
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
			var resMu stdsync.Mutex
			chunkResults := make(map[int64]error, len(chunk))

			for msg := range msgCh {
				uid := msg.UID
				batchAck.Add(1)
				if err := w.writer.Submit(ctx, IncomingMessage{
					AccountID: w.accountID, FolderID: folderID, FolderRole: role, UID: uid,
					Flags: msg.Flags, InternalAt: time.Unix(msg.Internal, 0), Raw: msg.Raw,
					IsResync: !notify,
					Ack:      batchAck.Done,
					OnDone: func(writeErr error, _ bool) {
						resMu.Lock()
						chunkResults[uid] = writeErr
						resMu.Unlock()
					},
				}); err != nil {
					batchAck.Done()
					return err
				}
			}
			fetchErr := <-errCh

			drained := make(chan struct{})
			go func() { batchAck.Wait(); close(drained) }()
			select {
			case <-drained:
			case <-ctx.Done():
				return ctx.Err()
			}

			// Advance checkpoint strictly across the contiguous prefix of chunk
			// UIDs that persisted cleanly. Any gap or write failure holds (or
			// lowers an inflated legacy checkpoint to) the first unpersisted
			// UID so the next pass retries it cleanly. Promotion to
			// serverUIDNext happens only after every chunk succeeds.
			var contiguousMax int64
			var firstUnpersistedUID int64
			var firstWriteErr error
			resMu.Lock()
			for _, uid := range chunk {
				wErr, submitted := chunkResults[uid]
				if !submitted {
					if fetchErr != nil {
						firstUnpersistedUID = uid
						break
					}
					continue
				}
				if wErr != nil {
					firstWriteErr = wErr
					firstUnpersistedUID = uid
					break
				}
				contiguousMax = uid
			}
			resMu.Unlock()

			ckpt := prev.UIDNext
			if firstUnpersistedUID > 0 {
				ckpt = firstUnpersistedUID
			} else if fetchErr == nil && firstWriteErr == nil && len(chunk) > 0 {
				chunkNext := chunk[len(chunk)-1] + 1
				if ckpt == 0 || chunkNext > ckpt || end < len(liveUIDs) {
					ckpt = chunkNext
				}
			} else if contiguousMax > 0 && contiguousMax+1 > ckpt {
				ckpt = contiguousMax + 1
			}
			if ckpt != prev.UIDNext {
				prev.UIDNext = ckpt
				if _, err := w.store.UpsertFolder(ctx, storage.FolderRow{
					AccountID: w.accountID, Name: name, Delimiter: prev.Delimiter, Role: rolePtr,
					UIDValidity: state.UIDValidity, UIDNext: ckpt, LastSyncedAt: prev.LastSyncedAt,
				}); err != nil {
					return err
				}
			}

			if fetchErr != nil {
				return fetchErr
			}
			if firstWriteErr != nil {
				return firstWriteErr
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

	finalUIDNext := serverUIDNext
	if prev.UIDNext > finalUIDNext {
		finalUIDNext = prev.UIDNext
	}
	now := time.Now().Unix()
	if _, err := w.store.UpsertFolder(ctx, storage.FolderRow{
		AccountID: w.accountID, Name: name, Delimiter: prev.Delimiter, Role: rolePtr,
		UIDValidity: state.UIDValidity, UIDNext: finalUIDNext, LastSyncedAt: &now,
	}); err != nil {
		return err
	}
	// CONDSTORE flag-delta sweep at the end of every successful
	// syncFolder pass — initial bulk, IDLE post-EXISTS, runPoll all
	// funnel through here. No-op when the server doesn't support
	// CONDSTORE or when the watermark is current.
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
// Returns the number of rows whose flags actually changed.
func (w *AccountWorker) syncFlagDeltas(ctx context.Context, c *imap.Client, folderID int64, serverModSeq uint64) (int, error) {
	if serverModSeq == 0 {
		return 0, nil // server doesn't support CONDSTORE
	}

	// A response fetched before our STORE must be applied before its ack removes
	// the pending overlay. Otherwise a late server snapshot could undo the UI.
	w.syncMu.Lock()
	defer w.syncMu.Unlock()

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
