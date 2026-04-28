package sync

import (
	"context"
	"fmt"
	"log/slog"
	stdsync "sync"
	"strings"
	"time"

	"github.com/spk/spk-mail/internal/api"
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
	store     *storage.Store
	secrets   *secrets.Store
	writer    *StoreWriter
	em        *api.Emitter
	flagOps   chan FlagOp
	// syncMu serializes syncFolder per account: while one folder is being
	// fetched, IDLE/poll-driven syncs of other folders queue up rather than
	// running in parallel. This makes the per-account "Syncing X: done/total"
	// status line truthful (otherwise the last folder to emit progress would
	// hide ongoing work on another folder) and keeps server load steady — only
	// one bulk fetch in flight per account at a time.
	syncMu stdsync.Mutex
}

// NewAccountWorker constructs a worker. It does no I/O.
func NewAccountWorker(id int64, s *storage.Store, sec *secrets.Store, w *StoreWriter, em *api.Emitter) *AccountWorker {
	return &AccountWorker{
		accountID: id,
		store:     s,
		secrets:   sec,
		writer:    w,
		em:        em,
		flagOps:   make(chan FlagOp, 64),
	}
}

// SubmitFlagOp queues a flag operation for async UID STORE. It is non-blocking:
// if the queue is full (cap 64) the op is dropped with a warning.
func (w *AccountWorker) SubmitFlagOp(op FlagOp) {
	select {
	case w.flagOps <- op:
	default:
		slog.Warn("flag op dropped: queue full",
			"account_id", w.accountID,
			"folder_id", op.FolderUID.FolderID,
			"uid", op.FolderUID.UID,
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
			name := folderName(op.FolderUID.FolderID)
			if name == "" {
				// Folder wasn't in the startup snapshot (e.g. server-side
				// folder discovered after runOnce began). Refresh from the
				// store and look it up directly — without this, flag ops
				// against the new folder would be dropped until the worker
				// next restarts.
				if folders, err := w.store.ListFolders(runCtx, w.accountID); err == nil {
					for _, f := range folders {
						if f.ID == op.FolderUID.FolderID {
							name = f.Name
							synced = append(synced, syncedFolder{ID: f.ID, Name: f.Name, Role: roleStr(f.Role)})
							break
						}
					}
				}
			}
			if name == "" {
				slog.Warn("store flag dropped: unknown folder",
					"account_id", w.accountID, "folder_id", op.FolderUID.FolderID, "uid", op.FolderUID.UID)
				continue
			}
			if name != currentSel {
				if _, err := c.Select(runCtx, name); err != nil {
					slog.Warn("store flag select failed", "folder", name, "err", err)
					continue
				}
				currentSel = name
			}
			if err := c.StoreFlags(runCtx, op.FolderUID.UID, op.Flags, op.Add); err != nil {
				slog.Warn("store flag failed", "folder", name, "uid", op.FolderUID.UID, "err", err)
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
func (w *AccountWorker) syncFolder(ctx context.Context, c *imap.Client, folderID int64, name, role string, notify bool) error {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	state, err := c.Select(ctx, name)
	if err != nil {
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
		msgCh, errCh := c.FetchSinceUIDRange(ctx, cursor, batchEnd, true)
		for msg := range msgCh {
			if msg.UID > maxUID {
				maxUID = msg.UID
			}
			if err := w.writer.Submit(ctx, IncomingMessage{
				AccountID: w.accountID, FolderID: folderID, FolderRole: role, UID: msg.UID,
				Flags: msg.Flags, InternalAt: time.Unix(msg.Internal, 0), Raw: msg.Raw,
				// IsResync gates the MessageArrived event in StoreWriter: true
				// means "stored silently". Driven by the syncFolder caller's
				// notify flag, not by UIDValidity (UIDValidity only signals
				// "this is the very first fetch ever for this folder", not
				// "this fetch is a real-time arrival").
				IsResync: !notify,
			}); err != nil {
				return err
			}
		}
		if err := <-errCh; err != nil {
			return err
		}
		// Checkpoint after every batch — keeps partial progress if the next
		// batch dies. UIDValidity is set unconditionally so subsequent runs
		// don't treat the folder as fresh.
		if _, err := w.store.UpsertFolder(ctx, storage.FolderRow{
			AccountID: w.accountID, Name: name, Delimiter: prev.Delimiter, Role: rolePtr,
			UIDValidity: state.UIDValidity, UIDNext: maxUID,
		}); err != nil {
			return err
		}
		// Emit SyncProgress so the UI can show a per-account "Syncing
		// <folder>: done/total" status line. total is the server-side UIDNext
		// (next UID the server will assign on new messages); done is the
		// highest UID we have ingested so far. They converge as the bulk sync
		// catches up. For very small mailboxes total may be ~equal to done on
		// the first iteration — UI hides the line once done >= total.
		if w.em != nil {
			w.em.Emit(api.Event{Type: "SyncProgress", Payload: map[string]any{
				"account_id": w.accountID,
				"folder_id":  folderID,
				"folder":     name,
				"done":       maxUID,
				"total":      int64(state.UIDNext),
			}})
		}
		cursor = batchEnd
	}
	return nil
}

// fetchBatchSize bounds each UID FETCH range. 200 is a conservative balance:
// small enough that a single FETCH stays under server-side per-command
// timeouts even when the batch lands on messages with multi-MB attachments,
// large enough that a 100k-message mailbox completes in ~500 round-trips.
const fetchBatchSize int64 = 200

func (w *AccountWorker) runIDLE(ctx context.Context, acc storage.AccountRow, folder, role string) {
	pw, _ := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
	c, err := imap.Dial(ctx, imap.DialOpts{
		Host: acc.IMAPHost, Port: acc.IMAPPort,
		Username: acc.IMAPUsername, Password: string(pw), UseTLS: acc.UseTLS,
	})
	if err != nil {
		return
	}
	defer c.Close()
	if _, err := c.Select(ctx, folder); err != nil {
		return
	}
	notifs := make(chan imap.IdleNotification, 8)
	stop := c.Idle(ctx, notifs)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-notifs:
			if !ok {
				return
			}
			if n.Kind == imap.NotifExists {
				folders, _ := w.store.ListFolders(ctx, acc.ID)
				for _, f := range folders {
					if strings.EqualFold(f.Name, folder) {
						pw, _ := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
						syncC, err := imap.Dial(ctx, imap.DialOpts{
							Host: acc.IMAPHost, Port: acc.IMAPPort,
							Username: acc.IMAPUsername, Password: string(pw),
							UseTLS: acc.UseTLS,
						})
						if err == nil {
							// runIDLE post-EXISTS — these messages just landed
							// on the server while we were connected, so they
							// are real-time arrivals and should produce a
							// MessageArrived notification.
							_ = w.syncFolder(ctx, syncC, f.ID, folder, role, true)
							_ = syncC.Close()
						}
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

