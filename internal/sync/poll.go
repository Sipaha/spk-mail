package sync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/storage"
)

const defaultPollInterval = 30 * time.Second

type folderTarget struct {
	Name string
	Role string
}

func (w *AccountWorker) pollPeriod() time.Duration {
	if w.pollInterval > 0 {
		return w.pollInterval
	}
	return defaultPollInterval
}

// runPollLoop maintains a single shared periodic poll loop per account rather
// than spawning one concurrent IMAP connection per folder. It receives any
// inbox folder whose server rejected IDLE at runtime via fallbackCh, discovers
// newly created server folders periodically, and surfaces dial/sync errors on
// inbox or polled folders instead of swallowing them.
func (w *AccountWorker) runPollLoop(ctx context.Context, acc storage.AccountRow, initialTargets []folderTarget, fallbackCh <-chan folderTarget) {
	targets := make([]folderTarget, 0, len(initialTargets))
	seen := make(map[string]bool, len(initialTargets))
	addTarget := func(ft folderTarget) bool {
		key := strings.ToLower(ft.Name)
		if seen[key] {
			return false
		}
		seen[key] = true
		targets = append(targets, ft)
		return true
	}
	for _, ft := range initialTargets {
		addTarget(ft)
	}

	t := time.NewTicker(w.pollPeriod())
	defer t.Stop()

	ticks := 0
	for {
		immediate := false
		select {
		case <-ctx.Done():
			return
		case ft := <-fallbackCh:
			if addTarget(ft) {
				immediate = true
			}
		case <-t.C:
			ticks++
		}
		// Drain any additional fallback notifications queued simultaneously.
	drainFallback:
		for {
			select {
			case ft := <-fallbackCh:
				if addTarget(ft) {
					immediate = true
				}
			default:
				break drainFallback
			}
		}

		discoverFolders := ticks > 0 && ticks%10 == 0
		if len(targets) == 0 && !discoverFolders && !immediate {
			continue
		}

		w.runPollTick(ctx, acc, &targets, addTarget, discoverFolders)
	}
}

func (w *AccountWorker) runPollTick(
	ctx context.Context,
	acc storage.AccountRow,
	targets *[]folderTarget,
	addTarget func(folderTarget) bool,
	discoverFolders bool,
) {
	pw, err := w.secrets.Get(fmt.Sprintf("account:%d", acc.ID))
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("poll: secrets get failed", "account_id", acc.ID, "err", err)
			w.reportPollFailure(*targets, err)
		}
		return
	}

	c, err := w.dialConfiguredClient(ctx, acc, pw)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("poll: dial failed", "account_id", acc.ID, "err", err)
			w.reportPollFailure(*targets, err)
		}
		return
	}
	stopClose := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer func() {
		stopClose()
		_ = c.Close()
	}()

	if discoverFolders {
		listed, lerr := c.ListFolders(ctx)
		if lerr != nil {
			if ctx.Err() == nil {
				slog.Warn("poll: folder discovery failed", "account_id", acc.ID, "err", lerr)
				w.reportPollFailure(*targets, lerr)
			}
			return
		}
		for _, f := range listed {
			if f.NoSelect {
				continue
			}
			if _, uerr := w.upsertFolderInfo(ctx, acc.ID, f); uerr != nil {
				if ctx.Err() == nil {
					slog.Warn("poll: upsert discovered folder failed", "account_id", acc.ID, "folder", f.Name, "err", uerr)
				}
				continue
			}
			if f.Role != "trash" && f.Role != "spam" && f.Role != "inbox" {
				addTarget(folderTarget{Name: f.Name, Role: f.Role})
			}
		}
	}

	var hasInbox bool
	var inboxErr, otherErr error
	for _, ft := range *targets {
		if ctx.Err() != nil {
			return
		}
		if ft.Role == "inbox" {
			hasInbox = true
		}
		fid, ok := w.folderIDByName(ctx, acc.ID, ft.Name)
		if !ok {
			continue
		}
		notify := ft.Role == "inbox"
		if serr := w.syncFolder(ctx, c, fid, ft.Name, ft.Role, notify); serr != nil {
			if ctx.Err() != nil {
				return
			}
			if ft.Role == "inbox" {
				if inboxErr == nil {
					inboxErr = serr
				}
				slog.Warn("poll: inbox sync failed", "account_id", acc.ID, "folder", ft.Name, "err", serr)
			} else if imap.IsCommandRejection(serr) {
				slog.Warn("poll: folder rejected", "account_id", acc.ID, "folder", ft.Name, "err", serr)
			} else {
				if otherErr == nil {
					otherErr = serr
				}
				slog.Warn("poll: folder sync failed", "account_id", acc.ID, "folder", ft.Name, "err", serr)
			}
		}
	}

	if hasInbox {
		if inboxErr != nil {
			w.setInboxState("error", inboxErr.Error())
		} else {
			w.setInboxState("ok", "")
		}
	}
	if otherErr != nil {
		w.setPollState("error", otherErr.Error())
	} else {
		w.setPollState("ok", "")
	}
}

func (w *AccountWorker) reportPollFailure(targets []folderTarget, err error) {
	hasInbox := false
	for _, ft := range targets {
		if ft.Role == "inbox" {
			hasInbox = true
			break
		}
	}
	if hasInbox {
		w.setInboxState("error", err.Error())
	} else {
		w.setPollState("error", err.Error())
	}
}
