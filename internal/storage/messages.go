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
		FROM messages WHERE thread_id = ? ORDER BY date`, threadID)
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
		// SELECT current state of all candidate messages in one shot.
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
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		type cand struct {
			id, accountID, folderID, uid int64
			threadID                     *int64
			flags                        string
		}
		var cands []cand
		for rows.Next() {
			var c cand
			if err := rows.Scan(&c.id, &c.accountID, &c.folderID, &c.uid, &c.threadID, &c.flags); err != nil {
				rows.Close()
				return err
			}
			cands = append(cands, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		// For each candidate not already \Seen: append flag, UPDATE, record change.
		for _, c := range cands {
			var fl []string
			if err := json.Unmarshal([]byte(c.flags), &fl); err != nil {
				return fmt.Errorf("MarkMessagesRead: bad flags JSON for id %d: %w", c.id, err)
			}
			if slices.Contains(fl, `\Seen`) {
				continue
			}
			fl = append(fl, `\Seen`)
			b, err := json.Marshal(fl)
			if err != nil {
				return fmt.Errorf("MarkMessagesRead: marshal flags for id %d: %w", c.id, err)
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

		// Refresh thread stats for each affected thread (in the same tx).
		for tid := range threadSet {
			if err := updateThreadStats(ctx, tx, tid); err != nil {
				return err
			}
			out.ChangedThreadIDs = append(out.ChangedThreadIDs, tid)
		}
		return nil
	})
	if err != nil {
		// Discard out: it was mutated inside the tx closure but the tx rolled
		// back, so any Changed/ChangedThreadIDs entries point at writes that
		// never landed.
		return MarkReadOutcome{}, err
	}
	return out, nil
}
