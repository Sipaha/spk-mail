package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

type MessageRow struct {
	ID             int64
	AccountID      int64
	FolderID       int64
	UID            int64
	MessageID      *string
	InReplyTo      *string
	References     *string
	ThreadID       *int64
	Subject        *string
	FromAddr       *string
	ToAddrs        *string
	CcAddrs        *string
	Date           int64
	Flags          string
	HasAttachments bool
	SizeBytes      int64
	BodyText       *string
	BodyHTML       *string
}

func (s *Store) InsertMessage(ctx context.Context, m MessageRow) (int64, error) {
	res, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO messages(account_id,folder_id,uid,message_id,in_reply_to,references_,thread_id,
			subject,from_addr,to_addrs,cc_addrs,date,flags,has_attachments,size_bytes,body_text,body_html)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.AccountID, m.FolderID, m.UID, m.MessageID, m.InReplyTo, m.References, m.ThreadID,
		m.Subject, m.FromAddr, m.ToAddrs, m.CcAddrs, m.Date, m.Flags, boolToInt(m.HasAttachments), m.SizeBytes, m.BodyText, m.BodyHTML)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateFlagsByUID updates the JSON flags blob for a message identified
// by (folder_id, uid). Used by the flag-refresh sweep to mirror
// server-side flag changes (a message marked \Seen on the user's phone
// or webmail) into our local DB without re-fetching the body.
//
// Returns (msgID, threadID, true) if the row's flags column actually
// changed; (0, nil, false) if the message wasn't found OR if the flags
// were already identical (no-op writes are filtered out so the caller
// can short-circuit downstream events). The thread id is returned so
// the caller can recompute thread.unread_count after a \Seen flip
// without an extra round-trip.
func (s *Store) UpdateFlagsByUID(ctx context.Context, folderID, uid int64, flagsJSON string) (int64, *int64, bool, error) {
	var msgID int64
	var threadID *int64
	var existing string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, thread_id, flags FROM messages WHERE folder_id = ? AND uid = ?`,
		folderID, uid).Scan(&msgID, &threadID, &existing)
	if err != nil {
		return 0, nil, false, err
	}
	if existing == flagsJSON {
		return msgID, threadID, false, nil
	}
	if _, err := s.writeDB.ExecContext(ctx,
		`UPDATE messages SET flags = ? WHERE id = ?`, flagsJSON, msgID); err != nil {
		return 0, nil, false, err
	}
	return msgID, threadID, true, nil
}

func (s *Store) GetMessage(ctx context.Context, id int64) (MessageRow, error) {
	row := s.readDB.QueryRowContext(ctx, `
		SELECT id,account_id,folder_id,uid,message_id,in_reply_to,references_,thread_id,
			subject,from_addr,to_addrs,cc_addrs,date,flags,has_attachments,size_bytes,body_text,body_html
		FROM messages WHERE id = ?`, id)
	var m MessageRow
	var hasAtt int
	err := row.Scan(&m.ID, &m.AccountID, &m.FolderID, &m.UID, &m.MessageID, &m.InReplyTo, &m.References, &m.ThreadID,
		&m.Subject, &m.FromAddr, &m.ToAddrs, &m.CcAddrs, &m.Date, &m.Flags, &hasAtt, &m.SizeBytes, &m.BodyText, &m.BodyHTML)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageRow{}, ErrNotFound
	}
	m.HasAttachments = hasAtt != 0
	return m, err
}

func (s *Store) GetMessagesByThread(ctx context.Context, threadID int64) ([]MessageRow, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT id,account_id,folder_id,uid,message_id,in_reply_to,references_,thread_id,
			subject,from_addr,to_addrs,cc_addrs,date,flags,has_attachments,size_bytes,body_text,body_html
		FROM messages WHERE thread_id = ? ORDER BY date DESC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var hasAtt int
		if err := rows.Scan(&m.ID, &m.AccountID, &m.FolderID, &m.UID, &m.MessageID, &m.InReplyTo, &m.References, &m.ThreadID,
			&m.Subject, &m.FromAddr, &m.ToAddrs, &m.CcAddrs, &m.Date, &m.Flags, &hasAtt, &m.SizeBytes, &m.BodyText, &m.BodyHTML); err != nil {
			return nil, err
		}
		m.HasAttachments = hasAtt != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpdateFlags(ctx context.Context, id int64, flagsJSON string) error {
	_, err := s.writeDB.ExecContext(ctx, `UPDATE messages SET flags = ? WHERE id = ?`, flagsJSON, id)
	return err
}

// UpdateBodyHTML overwrites the cached HTML body of a message. Used by
// AllowRemoteForMessage to persist the unblocked HTML so subsequent reads see
// the same image-allowed version without re-running the unblocker.
func (s *Store) UpdateBodyHTML(ctx context.Context, id int64, html string) error {
	_, err := s.writeDB.ExecContext(ctx, `UPDATE messages SET body_html = ? WHERE id = ?`, html, id)
	return err
}

// FindThreadByMessageIDs returns thread_id for any existing message whose
// Message-ID matches one of the supplied references. The match is byte-exact;
// RFC 5322 Message-IDs are case-sensitive in the local-part. Used at insert
// time to attach a new message to an existing thread.
func (s *Store) FindThreadByMessageIDs(ctx context.Context, msgIDs []string) (int64, bool, error) {
	if len(msgIDs) == 0 {
		return 0, false, nil
	}
	q := `SELECT thread_id FROM messages WHERE thread_id IS NOT NULL AND message_id IN (`
	args := make([]any, 0, len(msgIDs))
	for i, m := range msgIDs {
		if i > 0 {
			q += ","
		}
		q += "?"
		args = append(args, m)
	}
	q += `) LIMIT 1`
	var tid int64
	err := s.readDB.QueryRowContext(ctx, q, args...).Scan(&tid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return tid, err == nil, err
}

// seenCandidate is the per-row state that markRowsAsSeen consumes.
type seenCandidate struct {
	id, accountID, folderID, uid int64
	threadID                     *int64
	flags                        string
}

// markRowsAsSeen applies \Seen to every candidate that doesn't already carry
// it. For each flipped row it appends a MarkReadChange to out.Changed and
// records the row's thread_id (if non-nil) in threadSet for downstream
// updateThreadStats. Used by both MarkMessagesRead (id-list scope) and
// MarkFolderMessagesRead (folder scope) — they diverge only in their SELECT
// shape, the rest of the work is identical.
func markRowsAsSeen(ctx context.Context, tx *sql.Tx, cands []seenCandidate, out *MarkReadOutcome, threadSet map[int64]struct{}) error {
	for _, c := range cands {
		var fl []string
		if err := json.Unmarshal([]byte(c.flags), &fl); err != nil {
			return fmt.Errorf("markRowsAsSeen: bad flags JSON for id %d: %w", c.id, err)
		}
		if slices.Contains(fl, `\Seen`) {
			// Defensive skip — required for the MarkMessagesRead path (the IN-clause
			// SELECT does not pre-filter by flag), redundant for MarkFolderMessagesRead
			// (its NOT EXISTS json_each filter excludes \Seen rows at the SELECT level).
			continue
		}
		fl = append(fl, `\Seen`)
		b, err := json.Marshal(fl)
		if err != nil {
			return fmt.Errorf("markRowsAsSeen: marshal flags for id %d: %w", c.id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE messages SET flags = ? WHERE id = ?`, string(b), c.id); err != nil {
			return err
		}
		out.Changed = append(out.Changed, MarkReadChange{
			MessageID: c.id, AccountID: c.accountID, FolderID: c.folderID,
			UID: c.uid, ThreadID: c.threadID,
		})
		if c.threadID != nil {
			threadSet[*c.threadID] = struct{}{}
		}
	}
	return nil
}

// refreshThreadStatsForSet runs updateThreadStats for every thread id in the
// set (inside the supplied tx) and appends each id to out.ChangedThreadIDs.
// Order of out.ChangedThreadIDs follows map iteration order — unspecified.
func refreshThreadStatsForSet(ctx context.Context, tx *sql.Tx, threadSet map[int64]struct{}, out *MarkReadOutcome) error {
	for tid := range threadSet {
		if err := updateThreadStats(ctx, tx, tid); err != nil {
			return err
		}
		out.ChangedThreadIDs = append(out.ChangedThreadIDs, tid)
	}
	return nil
}

// MarkMessagesRead marks all supplied message IDs as \Seen in a single
// writer transaction. Messages that already carry \Seen are skipped.
// Returns MarkReadOutcome with the per-message metadata needed by the API
// layer to emit MessageUpdated events and submit IMAP STORE flag ops.
func (s *Store) MarkMessagesRead(ctx context.Context, ids []int64) (MarkReadOutcome, error) {
	if len(ids) == 0 {
		return MarkReadOutcome{}, nil
	}
	var out MarkReadOutcome
	threadSet := make(map[int64]struct{})

	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		q := `SELECT id, account_id, folder_id, uid, thread_id, flags
		      FROM messages WHERE id IN (`
		args := make([]any, len(ids))
		for i, id := range ids {
			if i > 0 {
				q += ","
			}
			q += "?"
			args[i] = id
		}
		q += `)`
		cands, err := scanSeenCandidates(ctx, tx, q, args...)
		if err != nil {
			return err
		}
		if err := markRowsAsSeen(ctx, tx, cands, &out, threadSet); err != nil {
			return err
		}
		return refreshThreadStatsForSet(ctx, tx, threadSet, &out)
	})
	if err != nil {
		// Discard out: it was mutated inside the tx closure but the tx rolled
		// back, so any Changed/ChangedThreadIDs entries point at writes that
		// never landed.
		return MarkReadOutcome{}, err
	}
	return out, nil
}

// MarkFolderMessagesRead flips \Seen on every currently-unread message in
// the folder, in a single writer transaction. Already-\Seen rows are skipped
// (filtered at the SELECT level via NOT EXISTS json_each). Returns the same
// MarkReadOutcome shape as MarkMessagesRead so the API layer can fan out a
// single bulk IMAP STORE op + one SSE event without re-reading rows.
func (s *Store) MarkFolderMessagesRead(ctx context.Context, folderID int64) (MarkReadOutcome, error) {
	var out MarkReadOutcome
	threadSet := make(map[int64]struct{})

	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		cands, err := scanSeenCandidates(ctx, tx, `
			SELECT id, account_id, folder_id, uid, thread_id, flags
			FROM messages
			WHERE folder_id = ?
			  AND NOT EXISTS (SELECT 1 FROM json_each(flags) WHERE value = '\Seen')
		`, folderID)
		if err != nil {
			return err
		}
		if err := markRowsAsSeen(ctx, tx, cands, &out, threadSet); err != nil {
			return err
		}
		return refreshThreadStatsForSet(ctx, tx, threadSet, &out)
	})
	if err != nil {
		return MarkReadOutcome{}, err
	}
	return out, nil
}

// ToggleThreadFlagged toggles the IMAP \Flagged flag on a thread.
//   - If ANY message in the thread has \Flagged, remove it from every
//     message that does (Action="removed"). The "unstar" path.
//   - If no message has \Flagged, add it to the most-recent message
//     (Action="added"). The "star" path — Gmail/Outlook convention:
//     star-the-thread = star the latest reply, not the whole thread.
//   - If the thread has no messages, returns Action="noop" with an empty
//     Changed slice and no DB writes.
//
// Single writer tx. threads.has_flagged is refreshed via updateThreadStats
// before commit so the next ListThreads call reflects the new state.
func (s *Store) ToggleThreadFlagged(ctx context.Context, threadID int64) (FlagToggleOutcome, error) {
	var out FlagToggleOutcome
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		cands, err := scanSeenCandidates(ctx, tx, `
			SELECT id, account_id, folder_id, uid, thread_id, flags
			FROM messages
			WHERE thread_id = ?
			ORDER BY date DESC
		`, threadID)
		if err != nil {
			return err
		}
		if len(cands) == 0 {
			out.Action = "noop"
			return nil
		}

		// Pre-pass: does ANY message currently carry \Flagged?
		anyFlagged := false
		for _, c := range cands {
			var fl []string
			if err := json.Unmarshal([]byte(c.flags), &fl); err != nil {
				return fmt.Errorf("ToggleThreadFlagged: bad flags JSON for id %d: %w", c.id, err)
			}
			if slices.Contains(fl, `\Flagged`) {
				anyFlagged = true
				break
			}
		}

		if anyFlagged {
			out.Action = "removed"
			for _, c := range cands {
				var fl []string
				if err := json.Unmarshal([]byte(c.flags), &fl); err != nil {
					return fmt.Errorf("ToggleThreadFlagged: bad flags JSON for id %d: %w", c.id, err)
				}
				idx := slices.Index(fl, `\Flagged`)
				if idx < 0 {
					continue
				}
				fl = slices.Delete(fl, idx, idx+1)
				b, err := json.Marshal(fl)
				if err != nil {
					return fmt.Errorf("ToggleThreadFlagged: marshal flags for id %d: %w", c.id, err)
				}
				if _, err := tx.ExecContext(ctx,
					`UPDATE messages SET flags = ? WHERE id = ?`, string(b), c.id); err != nil {
					return err
				}
				out.Changed = append(out.Changed, FlagChange{
					MessageID: c.id, AccountID: c.accountID,
					FolderID:  c.folderID, UID: c.uid,
				})
			}
		} else {
			out.Action = "added"
			c := cands[0] // most recent by date DESC
			var fl []string
			if err := json.Unmarshal([]byte(c.flags), &fl); err != nil {
				return fmt.Errorf("ToggleThreadFlagged: bad flags JSON for id %d: %w", c.id, err)
			}
			fl = append(fl, `\Flagged`)
			b, err := json.Marshal(fl)
			if err != nil {
				return fmt.Errorf("ToggleThreadFlagged: marshal flags for id %d: %w", c.id, err)
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE messages SET flags = ? WHERE id = ?`, string(b), c.id); err != nil {
				return err
			}
			out.Changed = append(out.Changed, FlagChange{
				MessageID: c.id, AccountID: c.accountID,
				FolderID:  c.folderID, UID: c.uid,
			})
		}

		return updateThreadStats(ctx, tx, threadID)
	})
	if err != nil {
		// Discard out: it was mutated inside the tx closure but the tx
		// rolled back, so any Changed entries point at writes that never
		// landed.
		return FlagToggleOutcome{}, err
	}
	return out, nil
}

// scanSeenCandidates is the shared post-SELECT scan routine: takes a SQL
// query and materializes its result set into seenCandidates. The query MUST
// project columns in this exact order:
//   id, account_id, folder_id, uid, thread_id, flags
// Wrong column order yields silent scan miscorrelation (e.g. folderID lands
// in uid). Used by both MarkMessagesRead (id-list SELECT) and
// MarkFolderMessagesRead (folder-scope SELECT).
func scanSeenCandidates(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]seenCandidate, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cands []seenCandidate
	for rows.Next() {
		var c seenCandidate
		if err := rows.Scan(&c.id, &c.accountID, &c.folderID, &c.uid, &c.threadID, &c.flags); err != nil {
			return nil, err
		}
		cands = append(cands, c)
	}
	return cands, rows.Err()
}

// SetRawResult discriminates the three outcomes of SetMessageRawBlob
// so the caller can balance refcounts when a parallel lazy-fetch raced
// (each fetch's InsertOrIncBlob bumped the blob's refcount; only one
// of them actually plants a new reference in the messages row).
type SetRawResult int

const (
	// SetFresh: the slot was NULL and now points at blobID. Caller
	// must NOT decrement — the row newly references the blob.
	SetFresh SetRawResult = iota
	// SetReplaced: the slot pointed at a different blob. Caller MUST
	// DecBlobRef the returned prevBlobID.
	SetReplaced
	// SetNoop: the slot already pointed at THIS exact blobID. Caller
	// MUST DecBlobRef blobID — their InsertOrIncBlob bumped refcount,
	// but the row is not gaining a new reference.
	SetNoop
)

// SetMessageRawBlob atomically links a blob to a message's raw slot.
// See SetRawResult for the three cases. raw_captured_at is refreshed
// on every call (including SetNoop). The retention sweep keys on this
// column, so re-linking the same blob slides the window. Note: a
// "view" via the cache-hit path (api/raw.go::tryCached) does NOT call
// this function and does NOT slide the window — only the sync-time
// capture and lazy-fetch paths do. Missing message id surfaces as
// sql.ErrNoRows.
func (s *Store) SetMessageRawBlob(ctx context.Context, msgID, blobID, capturedAtUnix int64) (SetRawResult, int64, error) {
	var result SetRawResult
	var prev int64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var existing *int64
		if err := tx.QueryRowContext(ctx,
			`SELECT raw_blob_id FROM messages WHERE id = ?`, msgID).Scan(&existing); err != nil {
			return err
		}
		switch {
		case existing == nil:
			result = SetFresh
			_, err := tx.ExecContext(ctx,
				`UPDATE messages SET raw_blob_id = ?, raw_captured_at = ? WHERE id = ?`,
				blobID, capturedAtUnix, msgID)
			return err
		case *existing == blobID:
			result = SetNoop
			_, err := tx.ExecContext(ctx,
				`UPDATE messages SET raw_captured_at = ? WHERE id = ?`,
				capturedAtUnix, msgID)
			return err
		default:
			result = SetReplaced
			prev = *existing
			_, err := tx.ExecContext(ctx,
				`UPDATE messages SET raw_blob_id = ?, raw_captured_at = ? WHERE id = ?`,
				blobID, capturedAtUnix, msgID)
			return err
		}
	})
	return result, prev, err
}

// GetMessageRawBlob returns the blob id + sha for a message's raw
// slot, or found=false when raw_blob_id IS NULL. Missing message id
// surfaces as sql.ErrNoRows.
func (s *Store) GetMessageRawBlob(ctx context.Context, msgID int64) (int64, string, bool, error) {
	var bid *int64
	var sha *string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT m.raw_blob_id, b.sha256
		 FROM messages m
		 LEFT JOIN blobs b ON b.id = m.raw_blob_id
		 WHERE m.id = ?`, msgID).Scan(&bid, &sha)
	if err != nil {
		return 0, "", false, err
	}
	if bid == nil || sha == nil {
		return 0, "", false, nil
	}
	return *bid, *sha, true, nil
}

// ClearMessageRawBlob nulls raw_blob_id + raw_captured_at and returns
// the blob id that was set (so caller can DecBlobRef), or nil if the
// slot was already empty. Missing message id surfaces as sql.ErrNoRows.
func (s *Store) ClearMessageRawBlob(ctx context.Context, msgID int64) (*int64, error) {
	var prev *int64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`SELECT raw_blob_id FROM messages WHERE id = ?`, msgID).Scan(&prev); err != nil {
			return err
		}
		if prev == nil {
			return nil
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE messages SET raw_blob_id = NULL, raw_captured_at = NULL WHERE id = ?`, msgID)
		return err
	})
	return prev, err
}

// SweepExpiredRaw clears raw_blob_id + raw_captured_at on every row
// whose capture is older than cutoffUnix. Returns the prev blob ids
// so the caller can DecBlobRef each (existing SweepBlobs reclaims
// refcount=0 on the next pass). Single writer tx.
//
// The query reads the partial index idx_messages_raw_capture so the
// cost scales with expiring-captures, not with total messages.
func (s *Store) SweepExpiredRaw(ctx context.Context, cutoffUnix int64) ([]int64, error) {
	var cleared []int64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT id, raw_blob_id FROM messages
			 WHERE raw_blob_id IS NOT NULL
			   AND raw_captured_at IS NOT NULL
			   AND raw_captured_at < ?`, cutoffUnix)
		if err != nil {
			return err
		}
		type row struct{ msgID, blobID int64 }
		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.msgID, &r.blobID); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, r := range batch {
			if _, err := tx.ExecContext(ctx,
				`UPDATE messages SET raw_blob_id = NULL, raw_captured_at = NULL WHERE id = ?`,
				r.msgID); err != nil {
				return err
			}
			cleared = append(cleared, r.blobID)
		}
		return nil
	})
	return cleared, err
}
