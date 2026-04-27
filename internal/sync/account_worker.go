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
		if err := w.syncFolder(ctx, c, fid, f.Name, f.Role); err != nil {
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

func (w *AccountWorker) syncFolder(ctx context.Context, c *imap.Client, folderID int64, name, role string) error {
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

	msgCh, errCh := c.FetchSinceUID(ctx, prev.UIDNext, true)
	maxUID := prev.UIDNext
	for msg := range msgCh {
		if msg.UID > maxUID {
			maxUID = msg.UID
		}
		w.writer.Submit(IncomingMessage{
			AccountID: w.accountID, FolderID: folderID, FolderRole: role, UID: msg.UID,
			Flags: msg.Flags, InternalAt: time.Unix(msg.Internal, 0), Raw: msg.Raw,
			IsResync: prev.UIDValidity == 0,
		})
	}
	if err := <-errCh; err != nil {
		return err
	}

	rolePtr := prev.Role
	if _, err := w.store.UpsertFolder(ctx, storage.FolderRow{
		AccountID: w.accountID, Name: name, Delimiter: prev.Delimiter, Role: rolePtr,
		UIDValidity: state.UIDValidity, UIDNext: maxUID,
	}); err != nil {
		return err
	}
	return nil
}

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
							_ = w.syncFolder(ctx, syncC, f.ID, folder, role)
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
	t := time.NewTicker(60 * time.Second)
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
						_ = w.syncFolder(ctx, c, f.ID, folder, role)
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
