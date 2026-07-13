package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/storage"
)

// idleSessionMaxLifetime caps how long we keep one IMAP connection in
// IDLE before tearing it down and dialing a fresh one. 25 minutes
// sits inside the typical 28–30-min server / NAT inactivity cutoff.
// Each bounce runs a pre-IDLE syncFolder, which doubles as a
// defence-in-depth catchup against any push the previous session
// missed.
const idleSessionMaxLifetime = 25 * time.Minute

func (w *AccountWorker) runIDLE(ctx context.Context, acc storage.AccountRow, folder, role string) {
	// Cache the password once at goroutine start. The previous code re-fetched
	// from the secrets store on every EXISTS notification, which is wasted
	// keyring traffic for an account whose password hasn't rotated. If a
	// rotation does happen, supervise will bounce the worker (next IMAP auth
	// fails → connection error → Run returns → restart picks up the fresh
	// secret), so caching here doesn't pin a stale credential indefinitely.
	pw, err := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
	if err != nil {
		slog.Warn("IDLE: secrets get failed", "account_id", acc.ID, "err", err)
		return
	}
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
	var stopIdle func()
	stopIdle = c.Idle(sessionCtx, notifs)
	defer func() {
		if stopIdle != nil {
			stopIdle()
		}
	}()
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
				folderID, ok := w.folderIDByName(ctx, acc.ID, folder)
				if !ok {
					continue
				}
				// RFC 2177: client must issue DONE before FETCH on the
				// same connection. Reuse this session instead of dialing
				// a second connection per EXISTS.
				stopIdle()
				stopIdle = nil
				// runIDLE post-EXISTS — these messages just landed on the
				// server while we were connected, so they are real-time
				// arrivals and should produce a MessageArrived notification.
				if syncErr := w.syncFolder(ctx, c, folderID, folder, role, true); syncErr != nil {
					if ctx.Err() == nil {
						slog.Warn("IDLE post-EXISTS sync failed", "account_id", acc.ID, "folder", folder, "err", syncErr)
					}
					return
				}
				notifs = make(chan imap.IdleNotification, 8)
				stopIdle = c.Idle(sessionCtx, notifs)
				slog.Info("IDLE session resumed", "account_id", acc.ID, "folder", folder)
			}
		}
	}
}
