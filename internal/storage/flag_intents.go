package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"time"
)

// FlagIntent is a durable desired flag value. ID changes on every replacement,
// so acknowledging an in-flight STORE cannot erase a newer user decision.
// Message deletion cascades it away; UIDValidity binds it to one mailbox epoch.
type FlagIntent struct {
	ID, MessageID, FolderID, UID, UIDValidity int64
	FolderName, Flag                          string
	Add                                       bool
}

func queueFlagTx(ctx context.Context, tx *sql.Tx, messageID int64, flag string, add bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO flag_intents(message_id,flag,add_flag,uid_validity)
 SELECT m.id,?,?,f.uid_validity FROM messages m JOIN folders f ON f.id=m.folder_id WHERE m.id=?
 ON CONFLICT(message_id,flag) DO UPDATE SET id=excluded.id,add_flag=excluded.add_flag,uid_validity=excluded.uid_validity,retry_at=0,last_error=''`, flag, add, messageID)
	return err
}

func (s *Store) ListFlagIntents(ctx context.Context, accountID int64, limit int) ([]FlagIntent, error) {
	return s.listFlagIntents(ctx, accountID, limit, 1<<62)
}
func (s *Store) ListDueFlagIntents(ctx context.Context, accountID int64, limit int) ([]FlagIntent, error) {
	return s.listFlagIntents(ctx, accountID, limit, time.Now().Unix())
}
func (s *Store) listFlagIntents(ctx context.Context, accountID int64, limit int, cutoff int64) ([]FlagIntent, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT p.id,p.message_id,m.folder_id,m.uid,p.uid_validity,f.name,p.flag,p.add_flag
 FROM flag_intents p JOIN messages m ON m.id=p.message_id JOIN folders f ON f.id=m.folder_id
 WHERE m.account_id=? AND f.account_id=? AND p.retry_at<=? ORDER BY p.id LIMIT ?`, accountID, accountID, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlagIntent
	for rows.Next() {
		var p FlagIntent
		if err := rows.Scan(&p.ID, &p.MessageID, &p.FolderID, &p.UID, &p.UIDValidity, &p.FolderName, &p.Flag, &p.Add); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AckFlagIntents(ctx context.Context, ids []int64) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM flag_intents WHERE id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Overlay only locally requested flags; all unrelated server flags remain live.
// Must run inside the same writer transaction as the incoming flag update.
func overlayFlagIntents(ctx context.Context, tx *sql.Tx, messageID int64, flagsJSON string) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT flag,add_flag FROM flag_intents WHERE message_id=?`, messageID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var flags []string
	if err := json.Unmarshal([]byte(flagsJSON), &flags); err != nil {
		return "", err
	}
	for rows.Next() {
		var flag string
		var add bool
		if err := rows.Scan(&flag, &add); err != nil {
			return "", err
		}
		i := slices.Index(flags, flag)
		if add && i < 0 {
			flags = append(flags, flag)
		} else if !add && i >= 0 {
			flags = slices.Delete(flags, i, i+1)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	b, err := json.Marshal(flags)
	return string(b), err
}

// Defer only the rejected snapshot, never a newer edit replacing its ID.
func (s *Store) DeferFlagIntents(ctx context.Context, ids []int64, retryAt int64, detail string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE flag_intents SET retry_at=?,last_error=? WHERE id=?`, retryAt, detail, id); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) FlagIntentFailure(ctx context.Context, accountID int64) (string, error) {
	var detail string
	err := s.readDB.QueryRowContext(ctx, `SELECT p.last_error FROM flag_intents p JOIN messages m ON m.id=p.message_id WHERE m.account_id=? AND p.last_error<>'' ORDER BY p.id LIMIT 1`, accountID).Scan(&detail)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return detail, err
}
