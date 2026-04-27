package storage

import "context"

type ThreadRow struct {
	ID          int64
	SubjectNorm string
	LastDate    int64
	MsgCount    int64
	UnreadCount int64
	HasFlagged  bool
	HasAttach   bool
}

func (s *Store) InsertThread(ctx context.Context, t ThreadRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO threads(subject_norm,last_date,msg_count,unread_count,has_flagged,has_attach)
		VALUES (?,?,?,?,?,?)`,
		t.SubjectNorm, t.LastDate, t.MsgCount, t.UnreadCount, boolToInt(t.HasFlagged), boolToInt(t.HasAttach))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListThreadsRecent(ctx context.Context, limit, offset int) ([]ThreadRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,subject_norm,last_date,msg_count,unread_count,has_flagged,has_attach
		FROM threads ORDER BY last_date DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ThreadRow
	for rows.Next() {
		var t ThreadRow
		var fl, at int
		if err := rows.Scan(&t.ID, &t.SubjectNorm, &t.LastDate, &t.MsgCount, &t.UnreadCount, &fl, &at); err != nil {
			return nil, err
		}
		t.HasFlagged = fl != 0
		t.HasAttach = at != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateThreadStats recomputes counters from the messages table.
// Called by StoreWriter after each insert/update.
func (s *Store) UpdateThreadStats(ctx context.Context, threadID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE threads SET
			last_date = (SELECT COALESCE(MAX(date),0) FROM messages WHERE thread_id = ?),
			msg_count = (SELECT COUNT(*) FROM messages WHERE thread_id = ?),
			unread_count = (SELECT COUNT(*) FROM messages WHERE thread_id = ? AND flags NOT LIKE '%\Seen%'),
			has_flagged = CASE WHEN EXISTS(SELECT 1 FROM messages WHERE thread_id = ? AND flags LIKE '%\Flagged%') THEN 1 ELSE 0 END,
			has_attach  = CASE WHEN EXISTS(SELECT 1 FROM messages WHERE thread_id = ? AND has_attachments = 1) THEN 1 ELSE 0 END
		WHERE id = ?`,
		threadID, threadID, threadID, threadID, threadID, threadID)
	return err
}
