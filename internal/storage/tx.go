package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// dbtx is the subset of *sql.DB and *sql.Tx that the storage helpers need.
// It lets us reuse the same body of SQL on either a Store-level connection
// (autocommit) or inside a transaction.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// WithTx runs fn inside a single SQLite transaction. The closure must use the
// supplied *sql.Tx for ALL its writes; mixing s.db and the tx inside fn would
// deadlock because the Store is configured with MaxOpenConns=1.
//
// On any error from fn the tx is rolled back; on a clean return it is
// committed. A panic inside fn is rolled back and re-raised.
func (s *Store) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// MessageBundle is the atomic insert unit consumed by InsertParsedMessageBundle.
// ThreadID is optional: if zero, a new threads row is inserted.
type MessageBundle struct {
	Message     MessageRow
	Attachments []AttachmentRow

	// Thread inputs. If ExistingThreadID > 0 the message attaches to it;
	// otherwise a new thread is inserted with NewThread fields and the
	// generated id is written into Message.ThreadID.
	ExistingThreadID int64
	NewThread        ThreadRow
}

// InsertParsedMessageBundle inserts thread (if needed), message, and all
// attachments inside a single SQLite transaction, then runs UpdateThreadStats
// for the resulting thread within the same tx. Returns (messageID, threadID).
//
// If any step fails the entire tx is rolled back — no orphan thread, no
// orphan message-without-attachments. The caller can safely retry the bundle.
func (s *Store) InsertParsedMessageBundle(ctx context.Context, b MessageBundle) (int64, int64, error) {
	var msgID, threadID int64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		threadID = b.ExistingThreadID
		if threadID == 0 {
			id, err := insertThread(ctx, tx, b.NewThread)
			if err != nil {
				return fmt.Errorf("insert thread: %w", err)
			}
			threadID = id
		}
		m := b.Message
		m.ThreadID = &threadID
		id, err := insertMessage(ctx, tx, m)
		if err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
		msgID = id
		for _, a := range b.Attachments {
			a.MessageID = msgID
			if _, err := insertAttachment(ctx, tx, a); err != nil {
				return fmt.Errorf("insert attachment: %w", err)
			}
		}
		if err := updateThreadStats(ctx, tx, threadID); err != nil {
			return fmt.Errorf("update thread stats: %w", err)
		}
		return nil
	})
	return msgID, threadID, err
}

// DeleteMessagesByFolder deletes every message row for the given folder.
// Used by the sync layer when UIDVALIDITY changes — the server has reused
// UID space, so the local cache is forced to re-fetch.
func (s *Store) DeleteMessagesByFolder(ctx context.Context, folderID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE folder_id = ?`, folderID)
	return err
}

// MaxUIDByFolder returns the highest UID currently present in messages for the
// given folder, or 0 if the folder has none. Drives the resume-from-DB-max
// cursor in syncFolder so a partial bulk fetch survives a process restart
// without re-fetching messages already on disk.
func (s *Store) MaxUIDByFolder(ctx context.Context, folderID int64) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(uid), 0) FROM messages WHERE folder_id = ?`, folderID).Scan(&n)
	return n, err
}

// --- Tx-aware private helpers used by InsertParsedMessageBundle ---

func insertThread(ctx context.Context, exec dbtx, t ThreadRow) (int64, error) {
	res, err := exec.ExecContext(ctx, `
		INSERT INTO threads(subject_norm,last_date,msg_count,unread_count,has_flagged,has_attach)
		VALUES (?,?,?,?,?,?)`,
		t.SubjectNorm, t.LastDate, t.MsgCount, t.UnreadCount, boolToInt(t.HasFlagged), boolToInt(t.HasAttach))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func insertMessage(ctx context.Context, exec dbtx, m MessageRow) (int64, error) {
	res, err := exec.ExecContext(ctx, `
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

func insertAttachment(ctx context.Context, exec dbtx, a AttachmentRow) (int64, error) {
	res, err := exec.ExecContext(ctx, `
		INSERT INTO attachments(message_id,part_id,filename,content_type,size_bytes,sha256,local_path,downloaded_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		a.MessageID, a.PartID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, a.LocalPath, a.DownloadedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateThreadStats(ctx context.Context, exec dbtx, threadID int64) error {
	_, err := exec.ExecContext(ctx, `
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
