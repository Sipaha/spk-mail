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
