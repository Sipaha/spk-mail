package storage

import "context"

// AccountIsMuted returns true iff the given account belongs to a profile with
// muted = 1. Accounts with NULL profile_id (no profile) are NEVER muted.
func (s *Store) AccountIsMuted(ctx context.Context, accountID int64) (bool, error) {
	var muted int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(p.muted, 0)
		FROM accounts a
		LEFT JOIN profiles p ON p.id = a.profile_id
		WHERE a.id = ?`, accountID).Scan(&muted)
	if err != nil {
		return false, err
	}
	return muted != 0, nil
}

// TotalUnreadExcludingMuted returns the sum of unread inbox messages whose
// account's profile is NOT muted (NULL profile_id is treated as not-muted).
func (s *Store) TotalUnreadExcludingMuted(ctx context.Context) (int64, error) {
	var total int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM messages m
		JOIN folders  f ON m.folder_id  = f.id
		JOIN accounts a ON m.account_id = a.id
		LEFT JOIN profiles p ON p.id = a.profile_id
		WHERE f.role = 'inbox'
		  AND COALESCE(p.muted, 0) = 0
		  AND NOT EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Seen')`).
		Scan(&total)
	return total, err
}
