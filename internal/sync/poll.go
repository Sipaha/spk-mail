package sync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/storage"
)

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
