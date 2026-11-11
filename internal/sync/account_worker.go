package sync

import (
	"context"
	"fmt"
	"log/slog"
	stdsync "sync"
	"time"

	"github.com/spk/spk-mail/internal/events"
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
	em        *events.Emitter
	// status is the engine's status tracker. nil in tests that build a bare
	// worker — setStatus tolerates that (the tracker's methods are nil-safe).
	status    *statusTracker
	flagOps   chan flagop.Op
	flagError string
	// syncMu serializes syncFolder per account: while one folder is being
	// fetched, IDLE/poll-driven syncs of other folders queue up rather than
	// running in parallel. This makes the per-account "Syncing X: done/total"
	// status line truthful (otherwise the last folder to emit progress would
	// hide ongoing work on another folder) and keeps server load steady — only
	// one bulk fetch in flight per account at a time.
	syncMu stdsync.Mutex
}

// NewAccountWorker constructs a worker. It does no I/O.
func NewAccountWorker(id int64, s storage.Writer, sec *secrets.Store, w *StoreWriter, em *events.Emitter) *AccountWorker {
	return &AccountWorker{
		accountID: id,
		store:     s,
		secrets:   sec,
		writer:    w,
		em:        em,
		flagOps:   make(chan flagop.Op, 64),
	}
}

// setStatus records the worker's state with the engine (source of truth for
// ListAccounts) and notifies the UI. Both halves must happen together: the
// event can be dropped on a full subscriber, the tracker cannot.
func (w *AccountWorker) setStatus(state, detail string) {
	w.status.set(w.accountID, state, detail)
	payload := map[string]any{"account_id": w.accountID, "state": state}
	if detail != "" {
		payload["detail"] = detail
	}
	events.Emit(w.em, "AccountStatus", payload)
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
		w.setStatus("error", err.Error())
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

	w.setStatus("connecting", "")

	c, err := imap.Dial(runCtx, imap.DialOpts{
		Host: acc.IMAPHost, Port: acc.IMAPPort,
		Username: acc.IMAPUsername, Password: string(pw), UseTLS: acc.UseTLS,
	})
	if err != nil {
		return err
	}
	defer c.Close()
	stopClose := context.AfterFunc(runCtx, func() { _ = c.Close() })
	defer stopClose()

	w.setStatus("ok", "")
	w.flagError = ""

	folders, err := c.ListFolders(runCtx)
	if err != nil {
		return err
	}
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
			if !imap.IsCommandRejection(err) {
				return err
			}
			// An inaccessible/deleted mailbox must not prevent this account's
			// healthy mailboxes or durable flag outbox from making progress.
			slog.Warn("initial folder sync rejected", "account_id", w.accountID, "folder_id", fid, "err", err)
		}
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

	// The channel is a wake-up hint only. SQLite owns the queue, including
	// requests made without a worker and requests interrupted by process exit.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := w.flushFlagIntents(runCtx, c); err != nil {
			return err
		}
		select {
		case <-runCtx.Done():
			return nil
		case <-w.flagOps:
		case <-ticker.C:
		}
	}
}

// roleStr safely dereferences an optional role.
func roleStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
