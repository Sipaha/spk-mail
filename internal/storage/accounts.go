package storage

import (
	"context"
	"database/sql"
	"errors"
)

var ErrNotFound = errors.New("storage: row not found")

type AccountRow struct {
	ID           int64
	Name         string
	Email        string
	IMAPHost     string
	IMAPPort     int
	IMAPUsername string
	UseTLS       bool
	Color        string
	CreatedAt    int64
	// ProfileID, when non-nil, attaches the account to a profile in the
	// profiles table. Nullable to support migration backfill and the rare
	// "no profile" state during account creation.
	ProfileID *int64
}

func (s *Store) InsertAccount(ctx context.Context, a AccountRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts(name,email,imap_host,imap_port,imap_username,use_tls,color,created_at,profile_id)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		a.Name, a.Email, a.IMAPHost, a.IMAPPort, a.IMAPUsername, boolToInt(a.UseTLS), a.Color, a.CreatedAt, a.ProfileID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetAccount(ctx context.Context, id int64) (AccountRow, error) {
	var a AccountRow
	var useTLS int
	err := s.db.QueryRowContext(ctx,
		`SELECT id,name,email,imap_host,imap_port,imap_username,use_tls,color,created_at,profile_id
		 FROM accounts WHERE id = ?`, id).
		Scan(&a.ID, &a.Name, &a.Email, &a.IMAPHost, &a.IMAPPort, &a.IMAPUsername, &useTLS, &a.Color, &a.CreatedAt, &a.ProfileID)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountRow{}, ErrNotFound
	}
	a.UseTLS = useTLS != 0
	return a, err
}

func (s *Store) ListAccounts(ctx context.Context) ([]AccountRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,email,imap_host,imap_port,imap_username,use_tls,color,created_at,profile_id
		 FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountRow
	for rows.Next() {
		var a AccountRow
		var useTLS int
		if err := rows.Scan(&a.ID, &a.Name, &a.Email, &a.IMAPHost, &a.IMAPPort, &a.IMAPUsername, &useTLS, &a.Color, &a.CreatedAt, &a.ProfileID); err != nil {
			return nil, err
		}
		a.UseTLS = useTLS != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
