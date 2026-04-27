package sync

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/fsutil"
	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// AttachmentDownloader is a per-account background worker that drains the
// pending-attachments queue. It opens its own IMAP connection (so the main
// sync session can stay in IDLE), fetches each MIME part by UID, writes it
// atomically to disk under <rootDir>/<account_id>/<message_id>/<filename>,
// hashes the bytes, updates the attachments row, and emits AttachmentReady.
type AttachmentDownloader struct {
	accountID int64
	store     *storage.Store
	secrets   *secrets.Store
	em        *api.Emitter
	rootDir   string
}

// NewAttachmentDownloader constructs the worker. It performs no I/O.
func NewAttachmentDownloader(accountID int64, s *storage.Store, sec *secrets.Store, em *api.Emitter, rootDir string) *AttachmentDownloader {
	return &AttachmentDownloader{accountID: accountID, store: s, secrets: sec, em: em, rootDir: rootDir}
}

// Run drains the pending queue every 5 seconds until ctx is cancelled.
// Errors are logged and the worker keeps polling — failed rows stay pending
// (local_path IS NULL) and will be retried on the next tick.
func (d *AttachmentDownloader) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.runOnce(ctx)
		}
	}
}

func (d *AttachmentDownloader) runOnce(ctx context.Context) {
	pending, err := d.store.ListPendingAttachments(ctx, d.accountID, 50)
	if err != nil || len(pending) == 0 {
		return
	}

	acc, err := d.store.GetAccount(ctx, d.accountID)
	if err != nil {
		return
	}
	pw, err := d.secrets.Get(fmt.Sprintf("account:%d", d.accountID))
	if err != nil {
		return
	}

	c, err := imap.Dial(ctx, imap.DialOpts{
		Host: acc.IMAPHost, Port: acc.IMAPPort,
		Username: acc.IMAPUsername, Password: string(pw), UseTLS: acc.UseTLS,
	})
	if err != nil {
		slog.Warn("downloader dial", "err", err)
		return
	}
	defer c.Close()

	folders, _ := d.store.ListFolders(ctx, d.accountID)
	folderName := func(id int64) string {
		for _, f := range folders {
			if f.ID == id {
				return f.Name
			}
		}
		return ""
	}

	// Pending may span multiple folders; SELECT once per folder and run
	// through the rows belonging to that folder before switching.
	currentFolder := ""
	for _, p := range pending {
		fname := folderName(p.FolderID)
		if fname == "" {
			continue
		}
		if fname != currentFolder {
			if _, err := c.Select(ctx, fname); err != nil {
				slog.Warn("downloader select", "folder", fname, "err", err)
				continue
			}
			currentFolder = fname
		}
		body, err := c.FetchBodyPart(ctx, p.UID, p.PartID)
		if err != nil {
			slog.Warn("downloader fetch part", "att", p.AttachmentID, "err", err)
			continue
		}
		// p.Filename is attacker-controlled (Content-Disposition filename
		// from the email). filepath.Base strips any directory component so
		// "../../escape.bin" becomes "escape.bin" — preventing the join
		// below from escaping rootDir. Also reject empty/dot edge cases.
		safeName := filepath.Base(p.Filename)
		if safeName == "" || safeName == "." || safeName == "/" || safeName == ".." {
			slog.Warn("downloader unsafe filename", "att", p.AttachmentID, "filename", p.Filename)
			continue
		}
		path := filepath.Join(d.rootDir,
			strconv.FormatInt(d.accountID, 10),
			strconv.FormatInt(p.MessageID, 10),
			safeName)
		if err := fsutil.AtomicWrite(path, body, 0o600); err != nil {
			slog.Warn("downloader write", "att", p.AttachmentID, "err", err)
			continue
		}
		sum, _ := fsutil.SHA256Reader(bytes.NewReader(body))
		if err := d.store.UpdateAttachmentDownloaded(ctx, p.AttachmentID, path, sum, time.Now().Unix()); err != nil {
			slog.Warn("downloader update", "att", p.AttachmentID, "err", err)
			continue
		}
		d.em.Emit(api.Event{Type: "AttachmentReady", Payload: map[string]any{
			"attachment_id": p.AttachmentID,
			"message_id":    p.MessageID,
		}})
	}
}
