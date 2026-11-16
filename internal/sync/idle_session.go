package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/storage"
)

const (
	// idleSessionMaxLifetime caps how long one IMAP connection stays in IDLE
	// before rotating to a fresh session (inside typical 28-30m NAT/server cutoffs).
	defaultIdleSessionMaxLifetime = 25 * time.Minute
	// defaultIdleCatchupInterval triggers a lightweight DONE -> syncFolder -> IDLE
	// sweep inside an active session so a server that silently suppresses EXISTS
	// cannot leave the inbox stale for 25 minutes.
	defaultIdleCatchupInterval = 2 * time.Minute
)

var idleReconnectDelays = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

func (w *AccountWorker) idleSessionLifetime() time.Duration {
	if w.idleMaxLifetime > 0 {
		return w.idleMaxLifetime
	}
	return defaultIdleSessionMaxLifetime
}

func (w *AccountWorker) idleCatchupPeriod() time.Duration {
	if w.idleCatchupInterval > 0 {
		return w.idleCatchupInterval
	}
	return defaultIdleCatchupInterval
}

func (w *AccountWorker) runIDLE(ctx context.Context, acc storage.AccountRow, folder, role string, fallback chan<- folderTarget) {
	pw, err := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
	if err != nil {
		slog.Warn("IDLE: secrets get failed", "account_id", acc.ID, "err", err)
		w.setInboxState("error", err.Error())
		return
	}

	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		rejected, entered, sessionErr := w.runIDLESession(ctx, acc, pw, folder, role, func() {
			attempt = 0
			w.setInboxState("ok", "")
		})
		if ctx.Err() != nil {
			return
		}
		if rejected {
			slog.Warn("IDLE rejected by server; falling back to polling", "account_id", acc.ID, "folder", folder, "err", sessionErr)
			if fallback != nil {
				select {
				case fallback <- folderTarget{Name: folder, Role: role}:
				case <-ctx.Done():
					return
				}
			}
			return
		}
		if sessionErr == nil {
			// Normal idleSessionMaxLifetime rotation: dial next session immediately.
			attempt = 0
			continue
		}

		// If the active IDLE session dropped unexpectedly (e.g. server BYE/reset),
		// allow one fast 100ms reconnect before surfacing an error; any Dial or
		// pre-IDLE sync failure (entered=false) or second consecutive failure
		// surfaces the error immediately so the UI never lies with "Synced".
		var wait time.Duration
		if entered && attempt == 0 {
			w.setInboxState("connecting", "")
			wait = 100 * time.Millisecond
			attempt = 1
		} else {
			w.setInboxState("error", sessionErr.Error())
			idx := attempt
			if idx >= len(idleReconnectDelays) {
				idx = len(idleReconnectDelays) - 1
			}
			wait = idleReconnectDelays[idx]
			attempt++
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// runIDLESession holds one IMAP connection in IDLE for at most idleSessionLifetime.
// It returns:
//   - rejected: true if the server rejected IDLE with tagged NO/BAD (triggering poll fallback)
//   - entered:  true if IDLE was entered at least once during this session
//   - err:      nil on clean session expiry, otherwise the transport/sync error
func (w *AccountWorker) runIDLESession(
	ctx context.Context,
	acc storage.AccountRow,
	pw []byte,
	folder, role string,
	onEntered func(),
) (rejected bool, entered bool, err error) {
	sessionCtx, cancel := context.WithTimeout(ctx, w.idleSessionLifetime())
	defer cancel()

	c, err := w.dialConfiguredClient(sessionCtx, acc, pw)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("IDLE dial failed", "account_id", acc.ID, "err", err)
		}
		return false, false, err
	}
	stopClose := context.AfterFunc(sessionCtx, func() { _ = c.Close() })
	defer func() {
		stopClose()
		_ = c.Close()
	}()

	// Pre-IDLE catchup sync BEFORE entering IDLE.
	if folderID, ok := w.folderIDByName(sessionCtx, acc.ID, folder); ok {
		if syncErr := w.syncFolder(sessionCtx, c, folderID, folder, role, true); syncErr != nil {
			if ctx.Err() == nil && errors.Is(syncErr, context.DeadlineExceeded) && sessionCtx.Err() != nil {
				return false, false, nil
			}
			if ctx.Err() == nil {
				slog.Warn("IDLE pre-IDLE catchup sync failed", "account_id", acc.ID, "folder", folder, "err", syncErr)
			}
			return false, false, syncErr
		}
	}

	notifs := make(chan imap.IdleNotification, 8)
	stopIdle, err := c.IdleWithErr(sessionCtx, notifs)
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) && sessionCtx.Err() != nil {
			return false, false, nil
		}
		if imap.IsCommandRejection(err) {
			return true, false, err
		}
		if ctx.Err() == nil {
			slog.Warn("IDLE start failed", "account_id", acc.ID, "folder", folder, "err", err)
		}
		return false, false, err
	}
	defer func() {
		if stopIdle != nil {
			_ = stopIdle()
		}
	}()

	entered = true
	if onEntered != nil {
		onEntered()
	}
	slog.Info("IDLE session started", "account_id", acc.ID, "folder", folder)

	catchup := time.NewTimer(w.idleCatchupPeriod())
	defer catchup.Stop()

	resyncAndRearm := func() (bool, error) {
		if stopIdle != nil {
			if serr := stopIdle(); serr != nil {
				stopIdle = nil
				return false, serr
			}
			stopIdle = nil
		}
		if folderID, ok := w.folderIDByName(sessionCtx, acc.ID, folder); ok {
			if syncErr := w.syncFolder(sessionCtx, c, folderID, folder, role, true); syncErr != nil {
				return false, syncErr
			}
		}
		notifs = make(chan imap.IdleNotification, 8)
		var ierr error
		stopIdle, ierr = c.IdleWithErr(sessionCtx, notifs)
		if ierr != nil {
			return imap.IsCommandRejection(ierr), ierr
		}
		slog.Info("IDLE session resumed", "account_id", acc.ID, "folder", folder)
		return false, nil
	}

	for {
		select {
		case <-sessionCtx.Done():
			return false, entered, nil
		case n, ok := <-notifs:
			if !ok {
				var stopErr error
				if stopIdle != nil {
					stopErr = stopIdle()
					stopIdle = nil
				}
				if sessionCtx.Err() != nil && ctx.Err() == nil {
					return false, entered, nil
				}
				if stopErr == nil {
					stopErr = fmt.Errorf("IDLE connection closed")
				}
				if ctx.Err() == nil {
					slog.Warn("IDLE session dropped", "account_id", acc.ID, "folder", folder, "err", stopErr)
				}
				return false, entered, stopErr
			}
			if n.Kind == imap.NotifExists {
				slog.Info("IDLE EXISTS received", "account_id", acc.ID, "folder", folder)
				rej, rerr := resyncAndRearm()
				if rerr != nil {
					if sessionCtx.Err() != nil && ctx.Err() == nil {
						return false, entered, nil
					}
					if ctx.Err() == nil {
						slog.Warn("IDLE post-EXISTS sync failed", "account_id", acc.ID, "folder", folder, "err", rerr)
					}
					return rej, entered, rerr
				}
				resetTimer(catchup, w.idleCatchupPeriod())
			}
		case <-catchup.C:
			rej, rerr := resyncAndRearm()
			if rerr != nil {
				if sessionCtx.Err() != nil && ctx.Err() == nil {
					return false, entered, nil
				}
				if ctx.Err() == nil {
					slog.Warn("IDLE periodic catchup failed", "account_id", acc.ID, "folder", folder, "err", rerr)
				}
				return rej, entered, rerr
			}
			resetTimer(catchup, w.idleCatchupPeriod())
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
