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
	{version: 6, apply: applyMigrationV6},
	{version: 7, apply: applyMigrationV7},
	{version: 8, apply: applyMigrationV8},
	{version: 9, apply: applyMigrationV9},
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

// applyMigrationV6 introduces remote_urls_approved — a per-URL allowlist that
// lets GetThread auto-unblock images at view time across messages, so the
// user only has to click "Show remote content" once for a given URL (a
// company logo, a recurring CDN-served avatar, etc.) and future emails that
// reference the same URL render it without the gate.
func applyMigrationV6(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS remote_urls_approved (
			url         TEXT PRIMARY KEY,
			approved_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("v6 create remote_urls_approved: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (6, strftime('%s','now'))`); err != nil {
		return fmt.Errorf("v6 record version: %w", err)
	}
	return tx.Commit()
}

// applyMigrationV7 introduces the content-addressed blob store. Attachments
// previously kept their bytes at a per-message path (one file per attachment
// row, even when multiple emails reference the same logo / avatar / banner);
// after v7 the bytes live exactly once in <data>/blobs/aa/bb/<sha256> and the
// `attachments` row references a `blobs` entry by id. `blobs.refcount` tracks
// how many attachments point at the blob — the GC drops the file when it
// reaches zero.
//
// This migration only adds the schema. Backfill of existing per-message
// files into the content-addressed store happens in a separate migration
// (v8) so the schema change can be reasoned about independently and the
// (potentially expensive) byte rehash + copy doesn't block app startup
// when the schema-only step is enough.
func applyMigrationV7(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS blobs (
			id          INTEGER PRIMARY KEY,
			sha256      TEXT NOT NULL UNIQUE,
			size_bytes  INTEGER NOT NULL,
			refcount    INTEGER NOT NULL DEFAULT 0,
			created_at  INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("v7 create blobs: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_blobs_refcount ON blobs(refcount) WHERE refcount = 0`); err != nil {
		return fmt.Errorf("v7 index blobs.refcount: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE attachments ADD COLUMN blob_id INTEGER REFERENCES blobs(id)`); err != nil {
		if !isDuplicateColumnErr(err) {
			return fmt.Errorf("v7 add attachments.blob_id: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_attachments_blob ON attachments(blob_id)`); err != nil {
		return fmt.Errorf("v7 index attachments.blob_id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (7, strftime('%s','now'))`); err != nil {
		return fmt.Errorf("v7 record version: %w", err)
	}
	return tx.Commit()
}

// applyMigrationV8 adds folders.highest_modseq — the CONDSTORE
// (RFC 7162) watermark per mailbox. The sync layer feeds it back to
// the server as CHANGEDSINCE on subsequent FETCHes so server-side
// flag deltas (\Seen on phone, \Flagged in webmail) propagate at
// O(changed messages) instead of the previous "fetch FLAGS for
// last N UIDs" brute scan.
//
// Existing rows initialise to 0; the first SELECT after upgrade
// records the live value as the baseline. No back-scan happens —
// flag drift accumulated before the upgrade is intentionally NOT
// reconciled (any reconciliation strategy is brute-force by
// definition; the user accepts a one-time manual correction in
// exchange for a clean go-forward sync model).
func applyMigrationV8(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE folders ADD COLUMN highest_modseq INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !isDuplicateColumnErr(err) {
			return fmt.Errorf("v8 add folders.highest_modseq: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (8, strftime('%s','now'))`); err != nil {
		return fmt.Errorf("v8 record version: %w", err)
	}
	return tx.Commit()
}

// applyMigrationV9 introduces the raw RFC822 capture window. Each
// message can optionally reference a `blobs` row holding its raw
// bytes; raw_captured_at records when that link was established so
// the periodic sweep can drop links older than the retention window.
// Existing rows have NULL on both columns and are picked up by the
// lazy-fetch path on first click.
func applyMigrationV9(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE messages ADD COLUMN raw_blob_id INTEGER REFERENCES blobs(id) ON DELETE SET NULL`); err != nil {
		if !isDuplicateColumnErr(err) {
			return fmt.Errorf("v9 add raw_blob_id: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE messages ADD COLUMN raw_captured_at INTEGER`); err != nil {
		if !isDuplicateColumnErr(err) {
			return fmt.Errorf("v9 add raw_captured_at: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_messages_raw_capture
		   ON messages(raw_captured_at)
		   WHERE raw_blob_id IS NOT NULL`); err != nil {
		return fmt.Errorf("v9 partial index: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (9, strftime('%s','now'))`); err != nil {
		return fmt.Errorf("v9 record version: %w", err)
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
