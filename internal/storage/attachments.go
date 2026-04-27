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
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO attachments(message_id,part_id,filename,content_type,size_bytes,sha256,local_path,downloaded_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		a.MessageID, a.PartID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, a.LocalPath, a.DownloadedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListAttachmentsByMessage(ctx context.Context, msgID int64) ([]AttachmentRow, error) {
	rows, err := s.db.QueryContext(ctx, `
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
