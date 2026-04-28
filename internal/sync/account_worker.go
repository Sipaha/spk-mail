package sync

import (
	"context"
	"fmt"
	"log/slog"
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
//
// TODO(plan-7): the drain loop lives at the bottom of runOnce and is not entered
// when runOnce errors before reaching it; under sustained sync errors flagOps
// can fill faster than they drain. Move the drain loop into its own goroutine
// scoped to AccountWorker.Run so it survives runOnce restarts.
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
func (w *AccountWorker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := w.runOnce(ctx); err != nil {
			slog.Warn("account worker error", "account_id", w.accountID, "err", err)
			w.em.Emit(api.Event{Type: "AccountStatus", Payload: map[string]any{
				"account_id": w.accountID, "state": "error", "detail": err.Error(),
			}})
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff(err)):
			}
		}
	}
}

func (w *AccountWorker) runOnce(ctx context.Context) error {
	acc, err := w.store.GetAccount(ctx, w.accountID)
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

	c, err := imap.Dial(ctx, imap.DialOpts{
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

	folders, err := c.ListFolders(ctx)
	if err != nil {
		return err
	}
	for _, f := range folders {
		role := f.Role
		var rolePtr *string
		if role != "" {
			rolePtr = &role
		}
		fid, err := w.store.UpsertFolder(ctx, storage.FolderRow{
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
		if err := w.syncFolder(ctx, c, fid, f.Name, f.Role, false); err != nil {
			return err
		}
	}

	// IDLE on inbox-role folders for push notifications; periodic poll for
	// other folders so Sent/Drafts/Archive/custom catch new messages without
	// a process restart. Trash and Spam are skipped — their content is
	// rarely interesting in real time and polling them costs IMAP traffic.
	//
	// TODO(plan-7): IDLE/poll goroutines outlive runOnce restarts. On any error
	// path that re-enters runOnce, fresh IDLE/poll goroutines spawn while the
	// previous ones are still alive (only ctx.Done unblocks them). Move
	// spawning under a supervisor scope tied to runOnce iterations so they're
	// torn down on restart.
	for _, f := range folders {
		switch f.Role {
		case "inbox":
			if c.HasIDLE(ctx) {
				go w.runIDLE(ctx, acc, f.Name, f.Role)
			} else {
				go w.runPoll(ctx, acc, f.Name, f.Role)
			}
		case "trash", "spam":
			// skip — high-volume, low-signal
		default:
			// sent / drafts / archive / "" (custom) — periodic poll
			go w.runPoll(ctx, acc, f.Name, f.Role)
		}
	}

	// Drain flag ops on the primary session until ctx cancels.
	for {
		select {
		case <-ctx.Done():
			return nil
		case op := <-w.flagOps:
			if err := c.StoreFlags(ctx, op.FolderUID.UID, op.Flags, op.Add); err != nil {
				slog.Warn("store flag failed", "err", err)
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
		if _, err := w.store.DB().ExecContext(ctx, `DELETE FROM messages WHERE folder_id = ?`, folderID); err != nil {
			return err
		}
		prev.UIDNext = 0
	}

	// Batch fetch in chunks of fetchBatchSize UIDs. A single open-ended
	// "UID FETCH N:*" works for small mailboxes but breaks on huge ones
	// (>~3000 messages we observed Yandex closing the connection mid-stream).
	// Persisting maxUID after each batch makes partial sync survive crashes.
	rolePtr := prev.Role
	cursor := prev.UIDNext
	maxUID := cursor
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		batchEnd := cursor + fetchBatchSize
		msgCh, errCh := c.FetchSinceUIDRange(ctx, cursor, batchEnd, true)
		anySeen := false
		for msg := range msgCh {
			anySeen = true
			if msg.UID > maxUID {
				maxUID = msg.UID
			}
			w.writer.Submit(IncomingMessage{
				AccountID: w.accountID, FolderID: folderID, FolderRole: role, UID: msg.UID,
				Flags: msg.Flags, InternalAt: time.Unix(msg.Internal, 0), Raw: msg.Raw,
				// IsResync gates the MessageArrived event in StoreWriter: true
				// means "stored silently". Driven by the syncFolder caller's
				// notify flag, not by UIDValidity (UIDValidity only signals
				// "this is the very first fetch ever for this folder", not
				// "this fetch is a real-time arrival").
				IsResync: !notify,
			})
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
		if !anySeen {
			// No more messages above batchEnd — we're caught up.
			break
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

// backoff returns a coarse retry delay. The engine supervisor handles
// longer-term restart strategy.
func backoff(err error) time.Duration {
	_ = err
	return 5 * time.Second
}

// splitHostPortAddr is a thin re-export so test code can pass mock.Addr()
// through the imap helper without importing imap directly.
func splitHostPortAddr(addr string) (string, int) { return imap.SplitHostPort(addr) }
