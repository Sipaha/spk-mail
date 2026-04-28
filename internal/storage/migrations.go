package storage

import (
	"context"
	"database/sql"
	"fmt"
)

type migrationStep struct {
	version int
	apply   func(ctx context.Context, db *sql.DB) error
}

var migrationSteps = []migrationStep{
	{version: 1, apply: applyMigrationV1},
	{version: 2, apply: applyMigrationV2},
	{version: 3, apply: applyMigrationV3},
	{version: 4, apply: applyMigrationV4},
	{version: 5, apply: applyMigrationV5},
}

func applyMigrationV1(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema v1: %w", err)
	}
	return nil
}

func applyMigrationV2(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS profiles (
			id         INTEGER PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			color      TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("v2 create profiles: %w", err)
	}

	// Add accounts.profile_id if not present (idempotent).
	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE accounts ADD COLUMN profile_id INTEGER REFERENCES profiles(id)`); err != nil {
		if !isDuplicateColumnErr(err) {
			return fmt.Errorf("v2 add accounts.profile_id: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_accounts_profile ON accounts(profile_id)`); err != nil {
		return fmt.Errorf("v2 index: %w", err)
	}

	// Backfill: any account left unassigned gets attached to a Default profile.
	var unassigned int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE profile_id IS NULL`).Scan(&unassigned); err != nil {
		return fmt.Errorf("v2 count unassigned: %w", err)
	}
	if unassigned > 0 {
		var defaultID int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM profiles WHERE name = 'Default'`).Scan(&defaultID)
		if err == sql.ErrNoRows {
			res, err := tx.ExecContext(ctx,
				`INSERT INTO profiles(name, color, sort_order, created_at)
				 VALUES ('Default', '#3b82f6', 0, strftime('%s','now'))`)
			if err != nil {
				return fmt.Errorf("v2 insert Default profile: %w", err)
			}
			defaultID, _ = res.LastInsertId()
		} else if err != nil {
			return fmt.Errorf("v2 lookup Default profile: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE accounts SET profile_id = ? WHERE profile_id IS NULL`, defaultID); err != nil {
			return fmt.Errorf("v2 backfill: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (2, strftime('%s','now'))`); err != nil {
		return fmt.Errorf("v2 record version: %w", err)
	}

	return tx.Commit()
}

func applyMigrationV3(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE profiles ADD COLUMN muted INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !isDuplicateColumnErr(err) {
			return fmt.Errorf("v3 add profiles.muted: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (3, strftime('%s','now'))`); err != nil {
		return fmt.Errorf("v3 record version: %w", err)
	}
	return tx.Commit()
}

// applyMigrationV4 recomputes thread stats for every existing thread once.
// Earlier versions used a LIKE-based check for the \Seen flag that silently
// missed flags arriving in different escape forms; the result was that
// `unread_count` and `last_date` could drift on already-synced threads. The
// json_each rewrite (Plan 9) fixes the SQL going forward, but already-broken
// rows need a one-time pass — which is what this migration does. Cheap enough
// for tens of thousands of threads (single transaction; one UPDATE per row).
func applyMigrationV4(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM threads`)
	if err != nil {
		return fmt.Errorf("v4 list threads: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("v4 scan thread id: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()

	const recompute = `UPDATE threads SET
		last_date = (SELECT COALESCE(MAX(date),0) FROM messages WHERE thread_id = ?),
		msg_count = (SELECT COUNT(*) FROM messages WHERE thread_id = ?),
		unread_count = (SELECT COUNT(*) FROM messages m WHERE m.thread_id = ?
			AND NOT EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Seen')),
		has_flagged = CASE WHEN EXISTS(
			SELECT 1 FROM messages m WHERE m.thread_id = ?
			AND EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Flagged')
		) THEN 1 ELSE 0 END,
		has_attach  = CASE WHEN EXISTS(SELECT 1 FROM messages WHERE thread_id = ? AND has_attachments = 1) THEN 1 ELSE 0 END
		WHERE id = ?`
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, recompute, id, id, id, id, id, id); err != nil {
			return fmt.Errorf("v4 recompute thread %d: %w", id, err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (4, strftime('%s','now'))`); err != nil {
		return fmt.Errorf("v4 record version: %w", err)
	}
	return tx.Commit()
}

// applyMigrationV5 deletes orphan thread rows — those with no messages
// attached. They appear as ghost entries at the bottom of the inbox
// (subject="", last_date=0, msg_count=0) and are leftovers from earlier
// partial-write paths in StoreWriter.process where InsertThread succeeded
// but InsertMessage failed and never retried. Wrapped in the same migrations
// pipeline so it runs once and is recorded in schema_migrations.
func applyMigrationV5(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM threads WHERE id NOT IN (SELECT DISTINCT thread_id FROM messages WHERE thread_id IS NOT NULL)`); err != nil {
		return fmt.Errorf("v5 delete orphan threads: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (5, strftime('%s','now'))`); err != nil {
		return fmt.Errorf("v5 record version: %w", err)
	}
	return tx.Commit()
}

func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "duplicate column") || contains(s, "duplicate column name")
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
