package storage

import (
	"context"
	"database/sql"
	"errors"
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
	res, err := s.db.ExecContext(ctx, `
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

func (s *Store) ListMessagesByFolder(ctx context.Context, folderID int64, limit, offset int) ([]MessageRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,account_id,folder_id,uid,message_id,in_reply_to,references_,thread_id,
			subject,from_addr,to_addrs,cc_addrs,date,flags,has_attachments,size_bytes,body_text,body_html
		FROM messages WHERE folder_id = ? ORDER BY date DESC LIMIT ? OFFSET ?`, folderID, limit, offset)
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

func (s *Store) GetMessage(ctx context.Context, id int64) (MessageRow, error) {
	row := s.db.QueryRowContext(ctx, `
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
	rows, err := s.db.QueryContext(ctx, `
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
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET flags = ? WHERE id = ?`, flagsJSON, id)
	return err
}

// FindThreadByMessageIDs returns thread_id for any existing message whose Message-ID
// matches one of the supplied references (case-insensitive). Used at insert time
// to attach a new message to an existing thread.
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
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&tid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return tid, err == nil, err
}
