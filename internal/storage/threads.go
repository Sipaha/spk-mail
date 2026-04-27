package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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

// ThreadFilter mirrors the api-layer ThreadFilter shape but lives in storage to
// avoid an import cycle. All fields are optional and combine with AND semantics.
// Pointer fields treat nil as "no constraint"; bool fields treat false the same.
type ThreadFilter struct {
	AccountID  *int64
	FolderID   *int64
	ProfileID  *int64
	UnreadOnly bool
	HasFlagged bool
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

// ListThreads returns recent threads filtered by the supplied ThreadFilter.
// An empty filter behaves identically to listing all threads ordered by
// last_date desc. Account/folder/profile filters share a single EXISTS
// subquery joining messages -> accounts so we don't multiply rows; unread/
// flagged filters are direct columns on threads. All values are bound via
// placeholders.
func (s *Store) ListThreads(ctx context.Context, f ThreadFilter, limit, offset int) ([]ThreadRow, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		wheres []string
		args   []any
	)

	// account/folder/profile filters all join through messages -> accounts.
	needSubQ := f.AccountID != nil || f.FolderID != nil || f.ProfileID != nil
	if needSubQ {
		subAnd := []string{"m.thread_id = t.id"}
		if f.AccountID != nil {
			subAnd = append(subAnd, "m.account_id = ?")
			args = append(args, *f.AccountID)
		}
		if f.FolderID != nil {
			subAnd = append(subAnd, "m.folder_id  = ?")
			args = append(args, *f.FolderID)
		}
		if f.ProfileID != nil {
			subAnd = append(subAnd, "a.profile_id = ?")
			args = append(args, *f.ProfileID)
		}
		wheres = append(wheres,
			"EXISTS (SELECT 1 FROM messages m JOIN accounts a ON a.id = m.account_id WHERE "+
				strings.Join(subAnd, " AND ")+")")
	}
	if f.UnreadOnly {
		wheres = append(wheres, "t.unread_count > 0")
	}
	if f.HasFlagged {
		wheres = append(wheres, "t.has_flagged = 1")
	}

	q := `SELECT t.id, t.subject_norm, t.last_date, t.msg_count, t.unread_count, t.has_flagged, t.has_attach FROM threads t`
	if len(wheres) > 0 {
		q += " WHERE " + strings.Join(wheres, " AND ")
	}
	q += " ORDER BY t.last_date DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
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

// ListThreadsRecent is a thin wrapper around ListThreads with an empty filter.
func (s *Store) ListThreadsRecent(ctx context.Context, limit, offset int) ([]ThreadRow, error) {
	return s.ListThreads(ctx, ThreadFilter{}, limit, offset)
}

// ListThreadsByProfile is a thin wrapper around ListThreads that filters by
// profile. Kept for backwards compatibility with existing callers.
func (s *Store) ListThreadsByProfile(ctx context.Context, profileID *int64, limit, offset int) ([]ThreadRow, error) {
	return s.ListThreads(ctx, ThreadFilter{ProfileID: profileID}, limit, offset)
}

// UnreadCountsByFolder returns per-folder counts of unread (no \Seen flag)
// messages for the given account. Folders with zero unread messages are
// omitted from the result.
func (s *Store) UnreadCountsByFolder(ctx context.Context, accountID int64) (map[int64]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.folder_id, COUNT(*)
		FROM messages m
		WHERE m.account_id = ?
		  AND NOT EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Seen')
		GROUP BY m.folder_id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var fid, n int64
		if err := rows.Scan(&fid, &n); err != nil {
			return nil, err
		}
		out[fid] = n
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
