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

// InsertThread is test scaffolding: production writes go through
// InsertParsedMessageBundle (tx.go), which uses the unexported insertThread
// helper inside its single transaction so a thread is never inserted
// without the matching message row. Test files across multiple packages
// (internal/storage, internal/api) drive thread state through this entry
// point — keeping it exported is the price of not making them go through
// the bundle (which they don't actually want, since they're testing thread
// queries in isolation).
func (s *Store) InsertThread(ctx context.Context, t ThreadRow) (int64, error) {
	res, err := s.writeDB.ExecContext(ctx, `
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

	rows, err := s.readDB.QueryContext(ctx, q, args...)
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

// FolderCounts captures total/unread/flagged message counts for a single
// folder. Returned by MessageCountsByFolder.
type FolderCounts struct {
	Total   int64
	Unread  int64
	Flagged int64
}

// MessageCountsByFolder returns per-folder total/unread/flagged message counts
// for the given account. Folders with zero messages are omitted. Flag
// membership is checked via json_each(m.flags) so it is robust to any JSON
// escape encoding of the flags array.
func (s *Store) MessageCountsByFolder(ctx context.Context, accountID int64) (map[int64]FolderCounts, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT m.folder_id,
		       COUNT(*) AS total,
		       SUM(CASE WHEN NOT EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Seen') THEN 1 ELSE 0 END) AS unread,
		       SUM(CASE WHEN     EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Flagged') THEN 1 ELSE 0 END) AS flagged
		FROM messages m
		WHERE m.account_id = ?
		GROUP BY m.folder_id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]FolderCounts{}
	for rows.Next() {
		var fid, total, unread, flagged int64
		if err := rows.Scan(&fid, &total, &unread, &flagged); err != nil {
			return nil, err
		}
		out[fid] = FolderCounts{Total: total, Unread: unread, Flagged: flagged}
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
//
// The SQL body lives in the unexported tx.go::updateThreadStats helper so
// the same query is used whether the caller is autocommit (this method) or
// inside an InsertParsedMessageBundle transaction; keeping a single source
// avoids the two going out of sync.
func (s *Store) UpdateThreadStats(ctx context.Context, threadID int64) error {
	return updateThreadStats(ctx, s.writeDB, threadID)
}

// RecomputeAllThreadStats walks every thread row and re-runs UpdateThreadStats.
// Cheap on small mailboxes (single-digit ms per 100 threads) and run on every
// app boot so any thread with desynced unread_count / last_date / has_flagged /
// has_attach (e.g. left behind by an older LIKE-based query, or by a partial
// write) is repaired without user action.
func (s *Store) RecomputeAllThreadStats(ctx context.Context) error {
	rows, err := s.writeDB.QueryContext(ctx, `SELECT id FROM threads`)
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
// no usable References / In-Reply-To headers. It looks for a thread that owns
// at least one message in the SAME accountID with the same normalized subject
// whose last_date is within ±windowSeconds of dateUnix. The account scope
// prevents two unrelated "Re: Newsletter" messages in different accounts from
// silently merging into one thread (a cross-account privacy / profile leak).
// Returns the most recent match (ORDER BY last_date DESC) for determinism.
//
//	(0, false, nil) on miss; (id, true, nil) on hit; (0, false, err) on real DB
//
// errors so the caller can distinguish "no match" from "DB exploded".
func (s *Store) FindThreadBySubject(ctx context.Context, accountID int64, subjectNorm string, dateUnix, windowSeconds int64) (int64, bool, error) {
	if subjectNorm == "" {
		return 0, false, nil
	}
	from := dateUnix - windowSeconds
	to := dateUnix + windowSeconds
	var id int64
	err := s.readDB.QueryRowContext(ctx, `
		SELECT t.id FROM threads t
		WHERE t.subject_norm = ? AND t.last_date BETWEEN ? AND ?
		  AND EXISTS (SELECT 1 FROM messages m WHERE m.thread_id = t.id AND m.account_id = ?)
		ORDER BY t.last_date DESC LIMIT 1`,
		subjectNorm, from, to, accountID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}
