package storage

import "context"

type FolderRow struct {
	ID           int64
	AccountID    int64
	Name         string
	Delimiter    string
	Role         *string // inbox|sent|drafts|trash|spam|archive|nil
	UIDValidity  int64
	UIDNext      int64
	LastSyncedAt *int64
}

func (s *Store) UpsertFolder(ctx context.Context, f FolderRow) (int64, error) {
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO folders(account_id,name,delimiter,role,uid_validity,uid_next,last_synced_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(account_id,name) DO UPDATE SET
			delimiter=excluded.delimiter,
			role=excluded.role,
			uid_validity=excluded.uid_validity,
			uid_next=excluded.uid_next,
			last_synced_at=excluded.last_synced_at`,
		f.AccountID, f.Name, f.Delimiter, f.Role, f.UIDValidity, f.UIDNext, f.LastSyncedAt)
	if err != nil {
		return 0, err
	}
	var id int64
	// Re-read on writeDB so the SELECT sees the row we just upserted on this same
	// connection — going through readDB here would be a needless cross-connection
	// hop on the hot insert path.
	err = s.writeDB.QueryRowContext(ctx, `SELECT id FROM folders WHERE account_id=? AND name=?`, f.AccountID, f.Name).Scan(&id)
	return id, err
}

func (s *Store) ListFolders(ctx context.Context, accountID int64) ([]FolderRow, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT id,account_id,name,delimiter,role,uid_validity,uid_next,last_synced_at
		FROM folders WHERE account_id = ? ORDER BY name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderRow
	for rows.Next() {
		var f FolderRow
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Name, &f.Delimiter, &f.Role, &f.UIDValidity, &f.UIDNext, &f.LastSyncedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
