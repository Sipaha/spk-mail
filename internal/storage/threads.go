package storage

import (
	"context"
	"database/sql"
	"errors"
)

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

// ListThreadsByProfile returns recent threads, optionally filtered by profile.
// When profileID is nil, returns all threads (same as ListThreadsRecent).
// When non-nil, restricts to threads that have at least one message belonging
// to an account in that profile.
func (s *Store) ListThreadsByProfile(ctx context.Context, profileID *int64, limit, offset int) ([]ThreadRow, error) {
	if profileID == nil {
		return s.ListThreadsRecent(ctx, limit, offset)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.subject_norm, t.last_date, t.msg_count, t.unread_count, t.has_flagged, t.has_attach
		FROM threads t
		WHERE EXISTS (
			SELECT 1 FROM messages m
			JOIN accounts a ON a.id = m.account_id
			WHERE m.thread_id = t.id AND a.profile_id = ?
		)
		ORDER BY t.last_date DESC LIMIT ? OFFSET ?`, *profileID, limit, offset)
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
//
// The LIKE patterns look for the JSON-encoded flag tokens. The flags column
// stores a JSON array literal where each \-prefixed flag like \Seen is encoded
// with a literal escape (Go json.Marshal of "\Seen" produces the 8 bytes
// `"\\Seen"`). SQLite string literals don't interpret \\ as an escape, so the
// pattern '%"\\Seen"%' here represents the same 8-byte sequence and matches
// the real flag, while substring decoys like "Seenmaybe" or stray "Seen" do
// not (the surrounding " characters bound the token).
func (s *Store) UpdateThreadStats(ctx context.Context, threadID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE threads SET
			last_date = (SELECT COALESCE(MAX(date),0) FROM messages WHERE thread_id = ?),
			msg_count = (SELECT COUNT(*) FROM messages WHERE thread_id = ?),
			unread_count = (SELECT COUNT(*) FROM messages WHERE thread_id = ? AND flags NOT LIKE '%"\\Seen"%'),
			has_flagged = CASE WHEN EXISTS(SELECT 1 FROM messages WHERE thread_id = ? AND flags LIKE '%"\\Flagged"%') THEN 1 ELSE 0 END,
			has_attach  = CASE WHEN EXISTS(SELECT 1 FROM messages WHERE thread_id = ? AND has_attachments = 1) THEN 1 ELSE 0 END
		WHERE id = ?`,
		threadID, threadID, threadID, threadID, threadID, threadID)
	return err
}

// FindThreadBySubject returns a candidate thread bucket for a message that has
// no usable References / In-Reply-To headers. It looks for a thread with the
// same normalized subject whose last_date is within ±windowSeconds of dateUnix.
// Returns the most recent match (ORDER BY last_date DESC) for determinism.
//
//	(0, false, nil) on miss; (id, true, nil) on hit; (0, false, err) on real DB
//
// errors so the caller can distinguish "no match" from "DB exploded".
func (s *Store) FindThreadBySubject(ctx context.Context, subjectNorm string, dateUnix, windowSeconds int64) (int64, bool, error) {
	if subjectNorm == "" {
		return 0, false, nil
	}
	from := dateUnix - windowSeconds
	to := dateUnix + windowSeconds
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM threads
		WHERE subject_norm = ? AND last_date BETWEEN ? AND ?
		ORDER BY last_date DESC LIMIT 1`,
		subjectNorm, from, to).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}
