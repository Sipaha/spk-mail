package storage

import "context"

type AttachmentRow struct {
	ID           int64
	MessageID    int64
	PartID       string
	Filename     string
	ContentType  string
	SizeBytes    int64
	SHA256       *string
	LocalPath    *string
	DownloadedAt *int64
}

func (s *Store) InsertAttachment(ctx context.Context, a AttachmentRow) (int64, error) {
	res, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attachments(message_id,part_id,filename,content_type,size_bytes,sha256,local_path,downloaded_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		a.MessageID, a.PartID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, a.LocalPath, a.DownloadedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListAttachmentsByMessage(ctx context.Context, msgID int64) ([]AttachmentRow, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT id,message_id,part_id,filename,content_type,size_bytes,sha256,local_path,downloaded_at
		FROM attachments WHERE message_id = ? ORDER BY id`, msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttachmentRow
	for rows.Next() {
		var a AttachmentRow
		if err := rows.Scan(&a.ID, &a.MessageID, &a.PartID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.LocalPath, &a.DownloadedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
// for the given account, newest message first.
func (s *Store) ListPendingAttachments(ctx context.Context, accountID int64, limit int) ([]PendingAttachment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT a.id, a.message_id, m.account_id, m.folder_id, m.uid,
			a.part_id, a.filename, a.content_type, a.size_bytes
		FROM attachments a
		JOIN messages m ON m.id = a.message_id
		WHERE a.local_path IS NULL AND m.account_id = ?
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

func (s *Store) UpdateAttachmentDownloaded(ctx context.Context, id int64, localPath, sha256 string, ts int64) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE attachments SET local_path = ?, sha256 = ?, downloaded_at = ? WHERE id = ?`,
		localPath, sha256, ts, id)
	return err
}

func (s *Store) ClearAttachmentLocalPath(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE attachments SET local_path = NULL, downloaded_at = NULL WHERE id = ?`, id)
	return err
}

// GetAttachmentLocalPath returns the local filesystem path stored on an attachment
// row. found is true only when the column is non-null and non-empty; null/empty
// returns ("", false, nil) so callers distinguish "row missing local_path" from a
// real scan error. A missing row surfaces as sql.ErrNoRows from Scan.
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

// ListAttachmentsByMessages is the batch counterpart to ListAttachmentsByMessage.
// Real implementation lands in Task 2 of the split-connections plan.
func (s *Store) ListAttachmentsByMessages(ctx context.Context, msgIDs []int64) (map[int64][]AttachmentRow, error) {
	panic("ListAttachmentsByMessages: implementation pending — see plan Task 2")
}
