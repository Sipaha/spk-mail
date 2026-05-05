package sync

import (
	"bytes"
	"context"
	"log/slog"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/fsutil"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// AttachmentDownloader is a per-account background worker that drains the
// pending-attachments queue. It opens its own IMAP connection (so the main
// sync session can stay in IDLE), fetches each MIME part by UID, writes
// it into the content-addressed blob store at
// <dataDir>/blobs/<aa>/<bb>/<sha256>, points the attachments row at the
// resulting blob, and emits AttachmentReady. Same content arriving in
// multiple emails (company logos, recurring banners, vendor avatars)
// dedupes onto a single on-disk file via storage.InsertOrIncBlob.
type AttachmentDownloader struct {
	accountID int64
	store     storage.Writer
	secrets   *secrets.Store
	em        *api.Emitter
	dataDir   string // root of the on-disk blob tree (BlobPath joins under it)
}

// NewAttachmentDownloader constructs the worker. It performs no I/O.
// dataDir is the root the blob store lives under (BlobPath composes
// <dataDir>/blobs/<aa>/<bb>/<sha256>).
func NewAttachmentDownloader(accountID int64, s storage.Writer, sec *secrets.Store, em *api.Emitter, dataDir string) *AttachmentDownloader {
	return &AttachmentDownloader{accountID: accountID, store: s, secrets: sec, em: em, dataDir: dataDir}
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

	c, err := DialAccount(ctx, d.store, d.secrets, d.accountID)
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
		// Stream into the content-addressed store. WriteContentAddressed
		// hashes while writing so we get the digest + size for free; the
		// finalPath callback composes the git-style fan-out under
		// <dataDir>/blobs/. If another attachment with the same bytes
		// already landed (a recurring company logo, a vendor banner),
		// the existing on-disk file is reused — second writer's temp is
		// silently dropped.
		sha, size, err := fsutil.WriteContentAddressed(bytes.NewReader(body), func(s string) string {
			return storage.BlobPath(d.dataDir, s)
		})
		if err != nil {
			slog.Warn("downloader write blob", "att", p.AttachmentID, "err", err)
			continue
		}
		blobID, _, err := d.store.InsertOrIncBlob(ctx, sha, size, time.Now().Unix())
		if err != nil {
			slog.Warn("downloader insert blob", "att", p.AttachmentID, "err", err)
			continue
		}
		if err := d.store.UpdateAttachmentDownloaded(ctx, p.AttachmentID, blobID, sha, time.Now().Unix()); err != nil {
			slog.Warn("downloader update", "att", p.AttachmentID, "err", err)
			continue
		}
		d.em.Emit(api.Event{Type: "AttachmentReady", Payload: map[string]any{
			"attachment_id": p.AttachmentID,
			"message_id":    p.MessageID,
		}})
	}
}
