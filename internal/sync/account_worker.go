package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	stdsync "sync"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/flagop"
	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// AccountWorker drives one IMAP account: it connects, lists folders, performs
// the initial UID-based sync, then either IDLEs (on inbox-role folders when
// the server advertises IDLE) or polls. It also routes flag-mutation requests
// through to the live IMAP session.
type AccountWorker struct {
	accountID int64
	store     storage.Writer
	secrets   *secrets.Store
	writer    *StoreWriter
	em        *api.Emitter
	flagOps   chan flagop.Op
	// syncMu serializes syncFolder per account: while one folder is being
	// fetched, IDLE/poll-driven syncs of other folders queue up rather than
	// running in parallel. This makes the per-account "Syncing X: done/total"
	// status line truthful (otherwise the last folder to emit progress would
	// hide ongoing work on another folder) and keeps server load steady — only
	// one bulk fetch in flight per account at a time.
	syncMu stdsync.Mutex
}

// NewAccountWorker constructs a worker. It does no I/O.
func NewAccountWorker(id int64, s storage.Writer, sec *secrets.Store, w *StoreWriter, em *api.Emitter) *AccountWorker {
	return &AccountWorker{
		accountID: id,
		store:     s,
		secrets:   sec,
		writer:    w,
		em:        em,
		flagOps:   make(chan flagop.Op, 64),
	}
}

// SubmitFlagOp queues a flag operation for async UID STORE. It is non-blocking:
// if the queue is full (cap 64) the op is dropped with a warning. An empty
// UIDs slice is rejected at the boundary — the doc on flagop.Op states it
// must hold at least one UID, and accepting an empty slice would silently
// pass through to a no-op StoreFlags + a misleading "uids=[]" warning if
// the worker logged the dropped path.
func (w *AccountWorker) SubmitFlagOp(op flagop.Op) {
	if len(op.UIDs) == 0 {
		slog.Warn("flag op rejected: empty UIDs",
			"account_id", w.accountID, "folder_id", op.FolderID,
			"add", op.Add, "flags", op.Flags)
		return
	}
	select {
	case w.flagOps <- op:
	default:
		slog.Warn("flag op dropped: queue full",
			"account_id", w.accountID,
			"folder_id", op.FolderID,
			"uids", op.UIDs,
			"add", op.Add,
			"flags", op.Flags)
	}
}

// Run is the worker main loop. It blocks until ctx is cancelled. Errors during
// connect/sync trigger a coarse 5s backoff before retrying.
// Run drives one runOnce iteration and returns on error so that the engine's
// supervise loop can apply its tiered exponential backoff (1s → 2s → 5s →
// 15s → 60s → 300s, see engine.go). If we kept retrying internally with a
// fixed 5-second sleep, the supervisor's tier table would be dead code and
// a permanent network outage would produce a tight 5s reconnect loop.
func (w *AccountWorker) Run(ctx context.Context) {
	if err := w.runOnce(ctx); err != nil {
		slog.Warn("account worker error", "account_id", w.accountID, "err", err)
		w.em.Emit(api.Event{Type: "AccountStatus", Payload: map[string]any{
			"account_id": w.accountID, "state": "error", "detail": err.Error(),
		}})
	}
}

func (w *AccountWorker) runOnce(ctx context.Context) error {
	// runCtx is scoped to a single runOnce iteration. IDLE/poll goroutines
	// derive from it so a backoff-and-retry path tears them down before the
	// next iteration starts; without this, every error cycle would leak
	// O(folders) goroutines and IMAP connections.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	acc, err := w.store.GetAccount(runCtx, w.accountID)
	if err != nil {
		return err
	}
	pw, err := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
	if err != nil {
		return err
	}

	w.em.Emit(api.Event{Type: "AccountStatus", Payload: map[string]any{
		"account_id": acc.ID, "state": "connecting",
	}})

	c, err := imap.Dial(runCtx, imap.DialOpts{
		Host: acc.IMAPHost, Port: acc.IMAPPort,
		Username: acc.IMAPUsername, Password: string(pw), UseTLS: acc.UseTLS,
	})
	if err != nil {
		return err
	}
	defer c.Close()

	w.em.Emit(api.Event{Type: "AccountStatus", Payload: map[string]any{
		"account_id": acc.ID, "state": "ok",
	}})

	folders, err := c.ListFolders(runCtx)
	if err != nil {
		return err
	}
	type syncedFolder struct {
		ID   int64
		Name string
		Role string
	}
	synced := make([]syncedFolder, 0, len(folders))
	for _, f := range folders {
		role := f.Role
		var rolePtr *string
		if role != "" {
			rolePtr = &role
		}
		fid, err := w.store.UpsertFolder(runCtx, storage.FolderRow{
			AccountID: acc.ID, Name: f.Name, Delimiter: f.Delimiter,
			Role: rolePtr, UIDValidity: 0, UIDNext: 0,
		})
		if err != nil {
			return err
		}
		// Initial bulk sync — notifications are suppressed. The user just
		// started the app; flooding them with N notifications for N unread
		// messages from before this session is noise. Real-time arrivals
		// (runIDLE post-EXISTS) re-call syncFolder with notify=true.
		if err := w.syncFolder(runCtx, c, fid, f.Name, f.Role, false); err != nil {
			return err
		}
		synced = append(synced, syncedFolder{ID: fid, Name: f.Name, Role: f.Role})
	}

	// IDLE on inbox-role folders for push notifications; periodic poll for
	// other folders so Sent/Drafts/Archive/custom catch new messages without
	// a process restart. Trash and Spam are skipped — their content is
	// rarely interesting in real time and polling them costs IMAP traffic.
	for _, f := range folders {
		switch f.Role {
		case "inbox":
			if c.HasIDLE() {
				go w.runIDLE(runCtx, acc, f.Name, f.Role)
			} else {
				go w.runPoll(runCtx, acc, f.Name, f.Role)
			}
		case "trash", "spam":
			// skip — high-volume, low-signal
		default:
			// sent / drafts / archive / "" (custom) — periodic poll
			go w.runPoll(runCtx, acc, f.Name, f.Role)
		}
	}

	// Drain flag ops on the primary session until ctx cancels.
	//
	// Before each STORE we re-Select the folder that owns the UID. Without
	// this, STORE fires against whatever mailbox happened to be selected
	// last (the final folder synced above), which silently mutates flags on
	// the wrong message in multi-folder accounts.
	folderName := func(id int64) string {
		for _, f := range synced {
			if f.ID == id {
				return f.Name
			}
		}
		return ""
	}
	currentSel := ""
	for {
		select {
		case <-runCtx.Done():
			return nil
		case op := <-w.flagOps:
			name := folderName(op.FolderID)
			if name == "" {
				// Folder wasn't in the startup snapshot (e.g. server-side
				// folder discovered after runOnce began). Refresh from the
				// store and look it up directly — without this, flag ops
				// against the new folder would be dropped until the worker
				// next restarts.
				if folders, err := w.store.ListFolders(runCtx, w.accountID); err == nil {
					for _, f := range folders {
						if f.ID == op.FolderID {
							name = f.Name
							synced = append(synced, syncedFolder{ID: f.ID, Name: f.Name, Role: roleStr(f.Role)})
							break
						}
					}
				}
			}
			if name == "" {
				slog.Warn("store flag dropped: unknown folder",
					"account_id", w.accountID, "folder_id", op.FolderID, "uids", op.UIDs)
				continue
			}
			if name != currentSel {
				if _, err := c.Select(runCtx, name); err != nil {
					slog.Warn("store flag select failed", "folder", name, "err", err)
					continue
				}
				currentSel = name
			}
			if err := c.StoreFlags(runCtx, op.UIDs, op.Flags, op.Add); err != nil {
				slog.Warn("store flag failed", "folder", name, "uids", op.UIDs, "err", err)
			}
		}
	}
}

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
	slog.Info("syncFolder enter",
		"account_id", w.accountID, "folder", name, "notify", notify,
		"prev_uidnext", prev.UIDNext, "server_uidnext", state.UIDNext,
		"prev_modseq", prev.HighestModSeq, "server_modseq", state.HighestModSeq)

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
	// Persisting maxUID after each batch makes partial sync survive crashes.
	//
	// Termination is driven by the server's UIDNEXT (the next UID the server
	// will assign), not by per-batch emptiness: a sparse mailbox where UIDs
	// 51..399 were expunged but UID 400 is live would otherwise return an
	// empty batch [51,250] and the loop would exit without ever fetching
	// the live UIDs at 400+. We keep stepping until cursor reaches UIDNext.
	serverUIDNext := int64(state.UIDNext)
	rolePtr := prev.Role
	cursor := prev.UIDNext
	maxUID := cursor
	for cursor < serverUIDNext {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		batchEnd := cursor + fetchBatchSize
		if batchEnd > serverUIDNext {
			batchEnd = serverUIDNext
		}
		// Per-batch lock — see syncFolder doc comment. The closure form
		// keeps `defer Unlock()` straightforward across the four early-
		// return paths (Submit error, errCh err, drain ctx-done,
		// UpsertFolder err) without an explicit Unlock at every site.
		err := func() error {
			w.syncMu.Lock()
			defer w.syncMu.Unlock()
			msgCh, errCh := c.FetchSinceUIDRange(ctx, cursor, batchEnd, true)
			// batchAck tracks how many of this batch's messages are still
			// in-flight in the StoreWriter. We Wait on it before emitting
			// SyncProgress so the frontend can't see "all synced" before
			// the matching MessageInserted events have fired.
			var batchAck stdsync.WaitGroup
			for msg := range msgCh {
				if msg.UID > maxUID {
					maxUID = msg.UID
				}
				batchAck.Add(1)
				if err := w.writer.Submit(ctx, IncomingMessage{
					AccountID: w.accountID, FolderID: folderID, FolderRole: role, UID: msg.UID,
					Flags: msg.Flags, InternalAt: time.Unix(msg.Internal, 0), Raw: msg.Raw,
					// IsResync gates the MessageArrived event in StoreWriter: true
					// means "stored silently". Driven by the syncFolder caller's
					// notify flag, not by UIDValidity (UIDValidity only signals
					// "this is the very first fetch ever for this folder", not
					// "this fetch is a real-time arrival").
					IsResync: !notify,
					Ack:      batchAck.Done,
				}); err != nil {
					batchAck.Done() // never enqueued
					return err
				}
			}
			if err := <-errCh; err != nil {
				return err
			}
			// Wait for the writer to finish persisting every message in this
			// batch before we tell the UI we're done with it. Plain Wait()
			// would block on a stuck writer past ctx cancellation, so wrap it
			// in a goroutine + select.
			drained := make(chan struct{})
			go func() { batchAck.Wait(); close(drained) }()
			select {
			case <-drained:
			case <-ctx.Done():
				return ctx.Err()
			}
			// Checkpoint after every batch — keeps partial progress if the next
			// batch dies. UIDValidity is set unconditionally so subsequent runs
			// don't treat the folder as fresh. last_synced_at gives the UI a
			// "last synced N seconds ago" handle for per-folder status.
			//
			// UIDNext is the cursor's NEW position (batchEnd), not maxUID.
			// maxUID would be 0 for an empty/expunged folder so the next poll
			// would re-iterate the whole UID range from 0 again — emitting
			// "0 / serverUIDNext" repeatedly across hundreds of empty batches
			// for every Mailspring/Outbox/Drafts-template-style residual
			// folder. Recording batchEnd means "we have checked everything up
			// to this UID" and lets diff-sync skip the empty range next time.
			now := time.Now().Unix()
			if _, err := w.store.UpsertFolder(ctx, storage.FolderRow{
				AccountID: w.accountID, Name: name, Delimiter: prev.Delimiter, Role: rolePtr,
				UIDValidity: state.UIDValidity, UIDNext: batchEnd, LastSyncedAt: &now,
			}); err != nil {
				return err
			}
			// Emit SyncProgress so the UI can show a per-account "Syncing
			// <folder>: done/total" status line. total is the server-side UIDNext
			// (next UID the server will assign on new messages); done is the
			// cursor position — how far we've scanned, regardless of whether
			// the scanned UIDs were live messages or expunged tombstones.
			// They converge as the bulk sync catches up. UI hides the line
			// once done >= total.
			if w.em != nil {
				w.em.Emit(api.Event{Type: "SyncProgress", Payload: map[string]any{
					"account_id": w.accountID,
					"folder_id":  folderID,
					"folder":     name,
					"done":       batchEnd,
					"total":      int64(state.UIDNext),
				}})
			}
			return nil
		}()
		if err != nil {
			return err
		}
		cursor = batchEnd
	}
	// Apply CONDSTORE flag deltas at the end of every successful
	// syncFolder pass — initial bulk sync, IDLE post-EXISTS, and
	// runPoll all funnel through here, so this single hook keeps
	// flag state in sync across every code path that touches a
	// folder. syncFlagDeltas no-ops when the server doesn't support
	// CONDSTORE or when the watermark is already current; both are
	// cheap.
	if _, err := w.syncFlagDeltas(ctx, c, folderID, state.HighestModSeq); err != nil {
		slog.Warn("flag delta sync failed", "account_id", w.accountID, "folder", name, "err", err)
	}
	return nil
}

// fetchBatchSize bounds each UID FETCH range. 200 is a conservative balance:
// small enough that a single FETCH stays under server-side per-command
// timeouts even when the batch lands on messages with multi-MB attachments,
// large enough that a 100k-message mailbox completes in ~500 round-trips.
const fetchBatchSize int64 = 200

// idleSessionMaxLifetime caps how long we keep one IMAP connection in
// IDLE before tearing it down and dialing a fresh one.
//
// 5 minutes is short on purpose — it doubles as the safety-net catchup
// cadence: every bounce calls syncFolder before entering IDLE, so even
// if the server stops pushing EXISTS reliably (observed in the field
// against Yandex under load), the user sees at most 5 minutes of
// staleness instead of "waits until process restart". TCP+TLS+LOGIN is
// ~300ms of churn per bounce, negligible compared to the UX cost of
// missing mail.
//
// The previous 25-min value was sized to dodge NAT/router idle
// thresholds while minimising reconnects, but it leaned too hard on
// IDLE pushes actually arriving — and that turns out not to be a safe
// assumption in production.
const idleSessionMaxLifetime = 5 * time.Minute

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
		w.em.Emit(api.Event{Type: "MessageUpdated", Payload: map[string]any{
			"account_id": w.accountID,
			"folder_id":  folderID,
		}})
	}
	return changed, nil
}

func (w *AccountWorker) runIDLE(ctx context.Context, acc storage.AccountRow, folder, role string) {
	// Cache the password once at goroutine start. The previous code re-fetched
	// from the secrets store on every EXISTS notification, which is wasted
	// keyring traffic for an account whose password hasn't rotated. If a
	// rotation does happen, supervise will bounce the worker (next IMAP auth
	// fails → connection error → Run returns → restart picks up the fresh
	// secret), so caching here doesn't pin a stale credential indefinitely.
	pw, _ := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		w.runIDLESession(ctx, acc, pw, folder, role)
		// Brief backoff so a hard-failing dial doesn't busy-loop. The
		// supervise loop applies its own tier table when Run() returns
		// on a real error; this is the inner pause for the in-runIDLE
		// reconnect cycle.
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// runIDLESession holds one IMAP connection in IDLE for at most
// idleSessionMaxLifetime, then returns. Errors are surfaced via slog
// (not propagated) so the outer reconnect loop can keep going.
func (w *AccountWorker) runIDLESession(ctx context.Context, acc storage.AccountRow, pw []byte, folder, role string) {
	sessionCtx, cancel := context.WithTimeout(ctx, idleSessionMaxLifetime)
	defer cancel()

	c, err := imap.Dial(sessionCtx, imap.DialOpts{
		Host: acc.IMAPHost, Port: acc.IMAPPort,
		Username: acc.IMAPUsername, Password: string(pw), UseTLS: acc.UseTLS,
	})
	if err != nil {
		// Distinguish "real connect failure" from "parent ctx cancelled
		// while dialing" — the latter is the worker shutting down and
		// shouldn't generate a noisy log entry.
		if ctx.Err() == nil {
			slog.Warn("IDLE dial failed", "account_id", acc.ID, "err", err)
		}
		return
	}
	defer c.Close()

	// Catchup syncFolder BEFORE entering IDLE. Reasons:
	//
	//  1. New messages that arrived between the previous IDLE session
	//     ending (idleSessionMaxLifetime, the connection-bounce loop)
	//     and this session starting would otherwise wait for an
	//     EXISTS push that may never come. We saw this in the field:
	//     "новые письма появляются только при перезапуске".
	//  2. If the server stops pushing EXISTS reliably (Yandex has
	//     been observed to do this under load), the periodic IDLE
	//     bounce gives us a deterministic catchup cadence — at most
	//     idleSessionMaxLifetime of staleness instead of "waits
	//     until process restart".
	//
	// syncFolder also internally Selects with CondStore (when the
	// server supports it) and runs syncFlagDeltas at the end, so this
	// single call covers BOTH new-message fetch AND server-side flag
	// propagation. The sessionCtx scopes us to the IDLE-session
	// lifetime; if the user shuts down mid-catchup, the worker exits
	// cleanly.
	if folderID, ok := w.folderIDByName(ctx, acc.ID, folder); ok {
		if err := w.syncFolder(sessionCtx, c, folderID, folder, role, true); err != nil {
			if ctx.Err() == nil {
				slog.Warn("IDLE pre-IDLE catchup sync failed", "account_id", acc.ID, "folder", folder, "err", err)
			}
			return
		}
	}

	// syncFolder above already Selected the mailbox; the connection is
	// in SELECTED state and Idle() can be called directly. No need to
	// re-Select.
	notifs := make(chan imap.IdleNotification, 8)
	stop := c.Idle(sessionCtx, notifs)
	defer stop()
	slog.Info("IDLE session started", "account_id", acc.ID, "folder", folder)
	for {
		select {
		case <-sessionCtx.Done():
			return
		case n, ok := <-notifs:
			if !ok {
				return
			}
			if n.Kind == imap.NotifExists {
				// One-line breadcrumb so a "new mail not arriving"
				// report can be diagnosed from journalctl: if we never
				// log this, the server isn't pushing EXISTS (firewall,
				// IDLE timeout, server-side issue); if we do log it but
				// no MessageInserted follows, the failure is in the
				// post-EXISTS sync path.
				slog.Info("IDLE EXISTS received", "account_id", acc.ID, "folder", folder)
				folders, _ := w.store.ListFolders(ctx, acc.ID)
				for _, f := range folders {
					if strings.EqualFold(f.Name, folder) {
						syncC, err := imap.Dial(ctx, imap.DialOpts{
							Host: acc.IMAPHost, Port: acc.IMAPPort,
							Username: acc.IMAPUsername, Password: string(pw),
							UseTLS: acc.UseTLS,
						})
						if err != nil {
							slog.Warn("IDLE post-EXISTS dial failed", "account_id", acc.ID, "err", err)
							break
						}
						// runIDLE post-EXISTS — these messages just landed
						// on the server while we were connected, so they
						// are real-time arrivals and should produce a
						// MessageArrived notification.
						if syncErr := w.syncFolder(ctx, syncC, f.ID, folder, role, true); syncErr != nil {
							slog.Warn("IDLE post-EXISTS sync failed", "account_id", acc.ID, "folder", folder, "err", syncErr)
						}
						_ = syncC.Close()
						break
					}
				}
			}
		}
	}
}

func (w *AccountWorker) runPoll(ctx context.Context, acc storage.AccountRow, folder, role string) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			folders, _ := w.store.ListFolders(ctx, acc.ID)
			for _, f := range folders {
				if strings.EqualFold(f.Name, folder) {
					pw, _ := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
					c, err := imap.Dial(ctx, imap.DialOpts{
						Host: acc.IMAPHost, Port: acc.IMAPPort,
						Username: acc.IMAPUsername, Password: string(pw), UseTLS: acc.UseTLS,
					})
					if err == nil {
						// runPoll catches up on folders without IDLE. Could
						// see N new messages at once if several arrived
						// between ticks — keep notifications quiet here to
						// avoid bursts. The frontend's per-folder unread
						// badge still updates via SyncProgress / refresh.
						// syncFolder also runs CONDSTORE flag-delta sync at
						// the end, so server-side \Seen / \Flagged changes
						// on this folder propagate every poll tick without
						// any extra hook here.
						_ = w.syncFolder(ctx, c, f.ID, folder, role, false)
						_ = c.Close()
					}
				}
			}
		}
	}
}

// roleStr safely dereferences a *string role on a FolderRow, returning
// "" when nil. Used by the flag-ops drain loop when refreshing folder
// metadata mid-run.
func roleStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

