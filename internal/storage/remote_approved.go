package storage

import (
	"context"
	"database/sql"
)

// AddApprovedRemoteURLs records that the user has unblocked these URLs at
// least once, so future messages that reference any of them can be rendered
// with the image inline without forcing another "Show remote content" click.
// Idempotent: re-approving an already-approved URL is a no-op.
func (s *Store) AddApprovedRemoteURLs(ctx context.Context, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT OR IGNORE INTO remote_urls_approved(url, approved_at) VALUES (?, strftime('%s','now'))`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, u := range urls {
			if u == "" {
				continue
			}
			if _, err := stmt.ExecContext(ctx, u); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListApprovedRemoteURLs returns the full set of URLs the user has approved.
// Used at GetThread time to decide whether each `data-spk-original-src` URL
// should be auto-unblocked. The set is read once per GetThread call; with a
// realistic upper bound of a few thousand approved URLs the in-memory map is
// negligible compared to the message rows themselves.
func (s *Store) ListApprovedRemoteURLs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT url FROM remote_urls_approved`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out[u] = true
	}
	return out, rows.Err()
}
