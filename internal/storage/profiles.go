package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ProfileRow struct {
	ID        int64
	Name      string
	Color     string
	SortOrder int
	CreatedAt int64
	Muted     bool
}

// ErrProfileInUse is returned by DeleteProfile when at least one account is
// still attached to the profile. The frontend should prompt the user to move
// or delete those accounts first.
var ErrProfileInUse = errors.New("storage: profile has attached accounts")

// EnsureDefaultProfile guarantees that (a) at least one profile exists, and
// (b) every account is attached to a profile. Called from app startup; safe
// to invoke on every boot.
//
// Backfill rationale: account rows could end up with profile_id IS NULL in
// two ways — (1) during the v1→v2 migration window if someone added accounts
// while the schema was being mid-applied, and (2) historically, when the UI
// briefly shipped an "All" pseudo-profile that submitted addAccount with
// profile_id undefined. Re-running this on every boot reattaches such orphans
// to the Default profile so they remain visible in the per-profile sidebar.
func (s *Store) EnsureDefaultProfile(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Ensure a Default profile exists; remember its id.
	var defaultID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM profiles ORDER BY sort_order, id LIMIT 1`).Scan(&defaultID)
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := tx.ExecContext(ctx,
			`INSERT INTO profiles(name, color, sort_order, muted, created_at)
			 VALUES ('Default', '#3b82f6', 0, 0, strftime('%s','now'))`)
		if ierr != nil {
			return ierr
		}
		defaultID, _ = res.LastInsertId()
	} else if err != nil {
		return err
	}

	// 2. Reattach any orphan accounts (profile_id IS NULL) to Default.
	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET profile_id = ? WHERE profile_id IS NULL`, defaultID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) InsertProfile(ctx context.Context, p ProfileRow) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO profiles(name, color, sort_order, created_at, muted) VALUES (?,?,?,?,?)`,
		p.Name, p.Color, p.SortOrder, p.CreatedAt, boolToInt(p.Muted))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListProfiles(ctx context.Context) ([]ProfileRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, color, sort_order, created_at, muted FROM profiles ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileRow
	for rows.Next() {
		var p ProfileRow
		if err := rows.Scan(&p.ID, &p.Name, &p.Color, &p.SortOrder, &p.CreatedAt, &p.Muted); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProfile(ctx context.Context, id int64) (ProfileRow, error) {
	var p ProfileRow
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, color, sort_order, created_at, muted FROM profiles WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Color, &p.SortOrder, &p.CreatedAt, &p.Muted)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileRow{}, ErrNotFound
	}
	return p, err
}

// SetProfileMuted flips the muted flag on a profile. Muted profiles are
// excluded from desktop notifications and the tray badge total.
func (s *Store) SetProfileMuted(ctx context.Context, id int64, muted bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE profiles SET muted = ? WHERE id = ?`, boolToInt(muted), id)
	return err
}

func (s *Store) UpdateProfile(ctx context.Context, id int64, name, color string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE profiles SET name = ?, color = ? WHERE id = ?`, name, color, id)
	return err
}

func (s *Store) DeleteProfile(ctx context.Context, id int64) error {
	var attached int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE profile_id = ?`, id).Scan(&attached); err != nil {
		return err
	}
	if attached > 0 {
		return fmt.Errorf("%w: %d accounts attached to profile %d", ErrProfileInUse, attached, id)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM profiles WHERE id = ?`, id)
	return err
}

// ReorderProfiles updates sort_order for the given (profileID, sortOrder) pairs
// in a single transaction. The caller is responsible for passing every profile
// being reordered; profiles not in the list keep their current sort_order.
func (s *Store) ReorderProfiles(ctx context.Context, order map[int64]int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, n := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE profiles SET sort_order = ? WHERE id = ?`, n, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
