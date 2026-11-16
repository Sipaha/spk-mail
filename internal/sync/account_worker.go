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
// the initial UID-based sync (INBOX first so real-time arrivals are never held
// behind large secondary folders), then either IDLEs (on inbox-role folders
// when the server supports IDLE) or polls via a single shared poll loop. It
// also drains durable flag-mutation intents on the primary IMAP session.
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
	// running in parallel.
	syncMu stdsync.Mutex

	stateMu         stdsync.Mutex
	bootstrapState  string
	bootstrapDetail string
	inboxState      string
	inboxDetail     string
	pollState       string
	pollDetail      string
	lastPublished   AccountState
	hasPublished    bool

	// Optional test overrides; zero values select production defaults.
	idleMaxLifetime     time.Duration
	idleCatchupInterval time.Duration
	pollInterval        time.Duration
	commandTimeout      time.Duration
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

func (w *AccountWorker) publishStatusLocked(state, detail string) {
	cur := AccountState{State: state, Detail: detail}
	if w.hasPublished && w.lastPublished == cur {
		return
	}
	w.lastPublished = cur
	w.hasPublished = true
	w.status.set(w.accountID, state, detail)
	payload := map[string]any{"account_id": w.accountID, "state": state}
	if detail != "" {
		payload["detail"] = detail
	}
	events.Emit(w.em, "AccountStatus", payload)
}

func (w *AccountWorker) recomputeStatusLocked() {
	switch {
	case w.bootstrapState == "error":
		w.publishStatusLocked("error", w.bootstrapDetail)
	case w.inboxState == "error":
		w.publishStatusLocked("error", w.inboxDetail)
	case w.pollState == "error":
		w.publishStatusLocked("error", w.pollDetail)
	case w.flagError != "":
		w.publishStatusLocked("error", w.flagError)
	case w.bootstrapState == "connecting" || w.inboxState == "connecting":
		w.publishStatusLocked("connecting", "")
	case w.bootstrapState == "ok" || w.inboxState == "ok" || w.pollState == "ok":
		w.publishStatusLocked("ok", "")
	}
}

// setStatus forces the worker's published status (used for top-level Run errors).
func (w *AccountWorker) setStatus(state, detail string) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.bootstrapState = state
	w.bootstrapDetail = detail
	if state == "connecting" {
		w.inboxState = ""
		w.inboxDetail = ""
		w.pollState = ""
		w.pollDetail = ""
	}
	w.publishStatusLocked(state, detail)
}

func (w *AccountWorker) setBootstrapState(state, detail string) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.bootstrapState = state
	w.bootstrapDetail = detail
	w.recomputeStatusLocked()
}

func (w *AccountWorker) setInboxState(state, detail string) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.inboxState = state
	w.inboxDetail = detail
	w.recomputeStatusLocked()
}

func (w *AccountWorker) setPollState(state, detail string) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.pollState = state
	w.pollDetail = detail
	w.recomputeStatusLocked()
}

func (w *AccountWorker) setFlagError(detail string) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.flagError = detail
	w.recomputeStatusLocked()
}

func (w *AccountWorker) dialConfiguredClient(ctx context.Context, acc storage.AccountRow, pw []byte) (*imap.Client, error) {
	c, err := imap.Dial(ctx, imap.DialOpts{
		Host: acc.IMAPHost, Port: acc.IMAPPort,
		Username: acc.IMAPUsername, Password: string(pw), UseTLS: acc.UseTLS,
	})
	if err != nil {
		return nil, err
	}
	if w.commandTimeout > 0 {
		c.SetCommandTimeout(w.commandTimeout)
	}
	return c, nil
}

// Run drives one runOnce iteration and returns on error so that the engine's
// supervise loop can apply its tiered exponential backoff.
func (w *AccountWorker) Run(ctx context.Context) {
	if err := w.runOnce(ctx); err != nil {
		slog.Warn("account worker error", "account_id", w.accountID, "err", err)
		w.setStatus("error", err.Error())
	}
}

type syncedFolderEntry struct {
	id   int64
	info imap.FolderInfo
}

func (w *AccountWorker) runOnce(ctx context.Context) error {
	// runCtx is scoped to a single runOnce iteration. IDLE/poll goroutines
	// derive from it so a backoff-and-retry path tears them down before the
	// next iteration starts.
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
	w.setFlagError("")

	c, err := w.dialConfiguredClient(runCtx, acc, pw)
	if err != nil {
		return err
	}
	stopClose := context.AfterFunc(runCtx, func() { _ = c.Close() })
	defer func() {
		stopClose()
		_ = c.Close()
	}()

	folders, err := c.ListFolders(runCtx)
	if err != nil {
		return err
	}

	var inboxFolders, otherFolders []syncedFolderEntry
	for _, f := range folders {
		if f.NoSelect {
			continue
		}
		fid, err := w.upsertFolderInfo(runCtx, acc.ID, f)
		if err != nil {
			return err
		}
		entry := syncedFolderEntry{id: fid, info: f}
		if f.Role == "inbox" {
			inboxFolders = append(inboxFolders, entry)
		} else {
			otherFolders = append(otherFolders, entry)
		}
	}

	// Sync INBOX folders FIRST so real-time inbox sync is never starved behind
	// huge archive/sent folders, and never report "ok" until INBOX succeeds.
	for _, f := range inboxFolders {
		if err := w.syncFolder(runCtx, c, f.id, f.info.Name, f.info.Role, false); err != nil {
			return err
		}
	}

	hasIDLE := c.HasIDLEContext(runCtx)
	inboxFallbackCh := make(chan folderTarget, len(inboxFolders)+1)
	var pollTargets []folderTarget

	if len(inboxFolders) > 0 {
		w.setInboxState("ok", "")
	}
	w.setBootstrapState("ok", "")

	for _, f := range inboxFolders {
		if hasIDLE {
			go w.runIDLE(runCtx, acc, f.info.Name, f.info.Role, inboxFallbackCh)
		} else {
			pollTargets = append(pollTargets, folderTarget{Name: f.info.Name, Role: f.info.Role})
		}
	}
	for _, f := range otherFolders {
		switch f.info.Role {
		case "trash", "spam":
			// skip periodic poll — high-volume, low-signal
		default:
			pollTargets = append(pollTargets, folderTarget{Name: f.info.Name, Role: f.info.Role})
		}
	}

	// One shared poll loop per account prevents O(folders) simultaneous dials.
	go w.runPollLoop(runCtx, acc, pollTargets, inboxFallbackCh)

	// Initial bulk sync for secondary folders proceeds in the background of the
	// already-active INBOX watcher. Command rejections on individual non-inbox
	// folders are logged and skipped without poisoning the account.
	for _, f := range otherFolders {
		if err := w.flushFlagIntents(runCtx, c); err != nil {
			return err
		}
		if err := w.syncFolder(runCtx, c, f.id, f.info.Name, f.info.Role, false); err != nil {
			if !imap.IsCommandRejection(err) {
				return err
			}
			slog.Warn("initial folder sync rejected", "account_id", w.accountID, "folder_id", f.id, "err", err)
		}
	}

	// The channel is a wake-up hint only. SQLite owns the queue, including
	// requests made without a worker and requests interrupted by process exit.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if c.IsClosed() {
			pwFresh, serr := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
			if serr != nil {
				return serr
			}
			nextC, derr := w.dialConfiguredClient(runCtx, acc, pwFresh)
			if derr != nil {
				return derr
			}
			stopClose()
			_ = c.Close()
			c = nextC
			stopClose = context.AfterFunc(runCtx, func() { _ = c.Close() })
		}
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

func (w *AccountWorker) upsertFolderInfo(ctx context.Context, accountID int64, f imap.FolderInfo) (int64, error) {
	role := f.Role
	var rolePtr *string
	if role != "" {
		rolePtr = &role
	}
	return w.store.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accountID, Name: f.Name, Delimiter: f.Delimiter,
		Role: rolePtr, UIDValidity: 0, UIDNext: 0,
	})
}
