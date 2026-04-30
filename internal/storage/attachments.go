package storage

import (
	"context"
	"database/sql"
)

type AttachmentRow struct {
	ID           int64
	MessageID    int64
	PartID       string
	Filename     string
	ContentType  string
	SizeBytes    int64
	SHA256       *string
	LocalPath    *string // legacy: per-message path; new rows leave NULL and use BlobID
	BlobID       *int64  // FK to blobs(id) — populated once bytes are downloaded
	DownloadedAt *int64
}

func (s *Store) InsertAttachment(ctx context.Context, a AttachmentRow) (int64, error) {
	res, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attachments(message_id,part_id,filename,content_type,size_bytes,sha256,local_path,blob_id,downloaded_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		a.MessageID, a.PartID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, a.LocalPath, a.BlobID, a.DownloadedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type PendingAttachment struct {
	AttachmentID int64
	MessageID    int64
	AccountID    int64
	FolderID     int64
	UID          int64
	PartID       string
	Filename     string
	ContentType  string
	SizeBytes    int64
}

// ListPendingAttachments returns up to `limit` not-yet-downloaded attachments
// for the given account, newest message first. "Pending" is defined as
// blob_id IS NULL: legacy rows that still have a populated local_path are
// considered already-downloaded and won't be re-fetched. (Migration v8
// will reconcile legacy rows by hashing the file and pointing blob_id at
// the resulting blob, but that's a separate, idempotent pass.)
func (s *Store) ListPendingAttachments(ctx context.Context, accountID int64, limit int) ([]PendingAttachment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT a.id, a.message_id, m.account_id, m.folder_id, m.uid,
			a.part_id, a.filename, a.content_type, a.size_bytes
		FROM attachments a
		JOIN messages m ON m.id = a.message_id
		WHERE a.blob_id IS NULL AND a.local_path IS NULL AND m.account_id = ?
		ORDER BY m.date DESC
		LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingAttachment
	for rows.Next() {
		var p PendingAttachment
		if err := rows.Scan(&p.AttachmentID, &p.MessageID, &p.AccountID, &p.FolderID, &p.UID,
			&p.PartID, &p.Filename, &p.ContentType, &p.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateAttachmentDownloaded marks an attachment row as downloaded by
// pointing it at a blob. Caller is responsible for having already
// inserted/incremented the blob row via InsertOrIncBlob and written
// the bytes via fsutil.WriteContentAddressed.
//
// local_path is explicitly set to NULL: the new content-addressed
// store renders the legacy column meaningless for fresh rows, and
// keeping it nil avoids confusion when migration v8 backfills legacy
// rows (it keys off blob_id IS NULL AND local_path IS NOT NULL).
func (s *Store) UpdateAttachmentDownloaded(ctx context.Context, id int64, blobID int64, sha256 string, ts int64) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE attachments SET blob_id = ?, sha256 = ?, downloaded_at = ?, local_path = NULL WHERE id = ?`,
		blobID, sha256, ts, id)
	return err
}

// ClearAttachmentBlob clears the blob_id reference on an attachment row,
// optionally returning the blob_id that was cleared so the caller can
// schedule a DecBlobRef. Used when the on-disk file goes missing and we
// want to retry the download. Idempotent: clearing an already-cleared
// row is a no-op and returns (nil, nil).
//
// (Legacy local_path is also cleared so a row that has BOTH set —
// shouldn't happen in production but might in tests — gets reset to a
// clean pending state.)
func (s *Store) ClearAttachmentBlob(ctx context.Context, id int64) (*int64, error) {
	var prev *int64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT blob_id FROM attachments WHERE id = ?`, id)
		if err := row.Scan(&prev); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE attachments SET blob_id = NULL, local_path = NULL, downloaded_at = NULL WHERE id = ?`, id)
		return err
	})
	return prev, err
}

// GetAttachmentBlob returns the blob id and sha256 referenced by an
// attachment row. found=false when blob_id is NULL (not yet downloaded
// or migration v8 hasn't covered this legacy row yet). The caller
// composes the on-disk path via BlobPath(dataDir, sha256). Missing row
// surfaces as sql.ErrNoRows.
func (s *Store) GetAttachmentBlob(ctx context.Context, id int64) (blobID int64, sha256 string, found bool, err error) {
	var bid *int64
	var sha *string
	err = s.readDB.QueryRowContext(ctx,
		`SELECT a.blob_id, b.sha256
		 FROM attachments a
		 LEFT JOIN blobs b ON b.id = a.blob_id
		 WHERE a.id = ?`, id).Scan(&bid, &sha)
	if err != nil {
		return 0, "", false, err
	}
	if bid == nil || sha == nil {
		return 0, "", false, nil
	}
	return *bid, *sha, true, nil
}

// GetAttachmentLocalPath remains for legacy callers + migration v8. It
// returns the per-message path stored on pre-v7 rows; new code should
// use GetAttachmentBlob + BlobPath instead. Returns ("", false, nil)
// when local_path is null/empty.
func (s *Store) GetAttachmentLocalPath(ctx context.Context, id int64) (string, bool, error) {
	var lp *string
	if err := s.readDB.QueryRowContext(ctx, `SELECT local_path FROM attachments WHERE id = ?`, id).Scan(&lp); err != nil {
		return "", false, err
	}
	if lp == nil || *lp == "" {
		return "", false, nil
	}
	return *lp, true, nil
}

// ListAttachmentsByMessages returns a map of message ID → attachments for all
// given message IDs in a single query. Message IDs with no attachments are
// absent from the result map. Empty/nil input returns an empty map immediately.
func (s *Store) ListAttachmentsByMessages(ctx context.Context, msgIDs []int64) (map[int64][]AttachmentRow, error) {
	if len(msgIDs) == 0 {
		return map[int64][]AttachmentRow{}, nil
	}
	// Build placeholder list "?,?,?" — len(msgIDs) is bounded by the size of
	// a message thread (small, no need for batching).
	q := `SELECT id, message_id, part_id, filename, content_type, size_bytes, sha256, local_path, blob_id, downloaded_at
	      FROM attachments WHERE message_id IN (`
	args := make([]any, len(msgIDs))
	for i, id := range msgIDs {
		if i > 0 {
			q += ","
		}
		q += "?"
		args[i] = id
	}
	q += `) ORDER BY message_id, id`

	rows, err := s.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64][]AttachmentRow)
	for rows.Next() {
		var a AttachmentRow
		if err := rows.Scan(&a.ID, &a.MessageID, &a.PartID, &a.Filename,
			&a.ContentType, &a.SizeBytes, &a.SHA256, &a.LocalPath, &a.BlobID, &a.DownloadedAt); err != nil {
			return nil, err
		}
		out[a.MessageID] = append(out[a.MessageID], a)
	}
	return out, rows.Err()
}
