package storage

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/spk/spk-mail/internal/fsutil"
)

// BackfillLegacyAttachments rehashes pre-v7 per-message attachment
// files into the content-addressed blob store. Idempotent: each call
// picks up only rows where blob_id IS NULL AND local_path IS NOT NULL,
// so a re-run after a partial completion just continues where it left
// off. Safe to call on every startup.
//
// For each candidate row:
//  1. Open the legacy file at local_path. If missing → clear local_path
//     so the row falls back into pending and the downloader re-fetches.
//  2. WriteContentAddressed copies the bytes into <dataDir>/blobs/...
//     and yields the sha256.
//  3. InsertOrIncBlob registers / increments the refcount.
//  4. UPDATE attachments SET blob_id = ?, sha256 = ?, local_path = NULL.
//  5. os.Remove the legacy file. If unlink fails we log but don't fail
//     the backfill — the file is now an orphan that the next run can
//     delete when the user does a manual cleanup, or it can be left in
//     place. The DB has already moved on.
//
// Returns the number of rows successfully migrated. Errors from
// individual rows are logged and do not abort the batch — one bad
// row shouldn't block backfilling the other 50000.
func (s *Store) BackfillLegacyAttachments(ctx context.Context, dataDir string) (migrated int, err error) {
	if dataDir == "" {
		return 0, errors.New("BackfillLegacyAttachments: dataDir is empty")
	}

	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, local_path FROM attachments
		 WHERE blob_id IS NULL AND local_path IS NOT NULL AND local_path != ''`)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id        int64
		localPath string
	}
	var pending []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.localPath); err != nil {
			rows.Close()
			return migrated, err
		}
		pending = append(pending, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return migrated, err
	}
	if len(pending) == 0 {
		return 0, nil
	}

	slog.Info("legacy attachments backfill started", "candidates", len(pending))

	for _, c := range pending {
		f, openErr := os.Open(c.localPath)
		if openErr != nil {
			if os.IsNotExist(openErr) {
				// Clear the dead path so the downloader re-fetches.
				_, _ = s.ClearAttachmentBlob(ctx, c.id)
				slog.Info("legacy attachment missing on disk; queued for re-download",
					"att_id", c.id, "path", c.localPath)
				continue
			}
			slog.Warn("legacy attachment open failed", "att_id", c.id, "err", openErr)
			continue
		}
		sha, size, wErr := fsutil.WriteContentAddressed(f, func(s string) string {
			return BlobPath(dataDir, s)
		})
		_ = f.Close()
		if wErr != nil {
			slog.Warn("legacy attachment write blob failed", "att_id", c.id, "err", wErr)
			continue
		}

		now := time.Now().Unix()
		blobID, _, ibErr := s.InsertOrIncBlob(ctx, sha, size, now)
		if ibErr != nil {
			slog.Warn("legacy attachment insert blob failed", "att_id", c.id, "err", ibErr)
			continue
		}
		if uErr := s.UpdateAttachmentDownloaded(ctx, c.id, blobID, sha, now); uErr != nil {
			// Compensating action: we just bumped the refcount but the
			// row update failed, so back the increment out. Otherwise a
			// retry on the next startup would inc again and the blob
			// would be over-counted forever.
			_, _ = s.DecBlobRef(ctx, blobID)
			slog.Warn("legacy attachment row update failed", "att_id", c.id, "err", uErr)
			continue
		}
		// Best-effort unlink of the original file. Failure is logged
		// but doesn't roll back the migration — the row is already
		// pointing at the blob, so the file is just an orphan now.
		if rmErr := os.Remove(c.localPath); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("legacy attachment unlink failed (orphan left on disk)",
				"att_id", c.id, "path", c.localPath, "err", rmErr)
		}
		migrated++
	}

	slog.Info("legacy attachments backfill complete", "migrated", migrated, "candidates", len(pending))
	return migrated, nil
}
