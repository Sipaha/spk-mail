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
	// LastFrom is the most recent message's from_addr; nil if the thread has
	// no messages yet (which can happen briefly between thread insert and
	// first-message insert).
	LastFrom *string
	// Snippet is the most recent message's body_text, server-side truncated
	// to ~200 chars. Nil if the most recent message has no plain-text body.
	Snippet *string
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

	// Correlated subqueries fetch the most recent message's from_addr and a
	// 200-char body_text prefix per thread. SUBSTR is computed server-side to
	// keep the JSON payload small; the frontend trims further to its visible
	// width. NULL columns surface as Go nil pointers via sql.NullString.
	q := `SELECT t.id, t.subject_norm, t.last_date, t.msg_count, t.unread_count, t.has_flagged, t.has_attach,
		(SELECT m.from_addr FROM messages m WHERE m.thread_id = t.id ORDER BY m.date DESC LIMIT 1) AS last_from,
		(SELECT SUBSTR(COALESCE(m.body_text, ''), 1, 200) FROM messages m WHERE m.thread_id = t.id ORDER BY m.date DESC LIMIT 1) AS snippet
		FROM threads t`
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
		var lastFrom, snippet sql.NullString
		if err := rows.Scan(&t.ID, &t.SubjectNorm, &t.LastDate, &t.MsgCount, &t.UnreadCount, &fl, &at, &lastFrom, &snippet); err != nil {
			return nil, err
		}
		t.HasFlagged = fl != 0
		t.HasAttach = at != 0
		if lastFrom.Valid {
			v := lastFrom.String
			t.LastFrom = &v
		}
		if snippet.Valid {
			v := snippet.String
			t.Snippet = &v
		}
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
// Flag membership uses json_each(m.flags) to compare flag tokens by exact
// string equality (e.g. value = '\Seen'). Earlier versions used LIKE patterns
// like '%"\\Seen"%', which depended on JSON's specific escape encoding and
// silently broke when flags arrived in a different escape form; switching to
// json_each makes the check stable regardless of JSON formatting.
func (s *Store) UpdateThreadStats(ctx context.Context, threadID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE threads SET
			last_date = (SELECT COALESCE(MAX(date),0) FROM messages WHERE thread_id = ?),
			msg_count = (SELECT COUNT(*) FROM messages WHERE thread_id = ?),
			unread_count = (SELECT COUNT(*) FROM messages m WHERE m.thread_id = ?
				AND NOT EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Seen')),
			has_flagged = CASE WHEN EXISTS(
				SELECT 1 FROM messages m WHERE m.thread_id = ?
				AND EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Flagged')
			) THEN 1 ELSE 0 END,
			has_attach  = CASE WHEN EXISTS(SELECT 1 FROM messages WHERE thread_id = ? AND has_attachments = 1) THEN 1 ELSE 0 END
		WHERE id = ?`,
		threadID, threadID, threadID, threadID, threadID, threadID)
	return err
}

// RecomputeAllThreadStats walks every thread row and re-runs UpdateThreadStats.
// Cheap on small mailboxes (single-digit ms per 100 threads) and run on every
// app boot so any thread with desynced unread_count / last_date / has_flagged /
// has_attach (e.g. left behind by an older LIKE-based query, or by a partial
// write) is repaired without user action.
func (s *Store) RecomputeAllThreadStats(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM threads`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.UpdateThreadStats(ctx, id); err != nil {
			return err
		}
	}
	return nil
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
