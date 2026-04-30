package storage

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

// BlobRow is the in-memory shape of a row in the `blobs` table — the
// content-addressed store that backs all attachment bytes. The same blob
// can be referenced by many `attachments.blob_id` rows; refcount tracks
// how many.
type BlobRow struct {
	ID        int64
	SHA256    string
	SizeBytes int64
	Refcount  int64
	CreatedAt int64
}

// BlobPath returns the on-disk location of a blob given the data
// directory root and its sha256 hex digest. Layout follows the git
// object-store convention: <data>/blobs/aa/bb/<sha256> where `aa` /
// `bb` are the first / second byte of the digest. This caps directory
// fan-out at 256 entries per level and keeps lookup O(1) regardless of
// the total number of blobs.
//
// The function does NOT touch the filesystem — callers compose the
// path then call os.MkdirAll on its parent before writing.
func BlobPath(dataDir, sha256 string) string {
	if len(sha256) < 4 {
		// Defensive: caller passed a malformed digest. Returning a
		// path under the data dir keeps the failure localized to this
		// call instead of escaping into an unexpected filesystem
		// location, and the subsequent os.Stat/Open will surface a
		// clear error.
		return filepath.Join(dataDir, "blobs", "_invalid", sha256)
	}
	return filepath.Join(dataDir, "blobs", sha256[0:2], sha256[2:4], sha256)
}

// InsertOrIncBlob is the canonical entry point for adding a new attachment
// payload to the store: if a blob with this sha256 already exists, its
// refcount is incremented; otherwise a fresh row is inserted with
// refcount = 1. Returns (blobID, isNew):
//
//   - isNew = true  → the caller has just become responsible for ensuring
//     the bytes exist on disk at BlobPath(...). For an
//     atomic-write helper that races multiple concurrent
//     callers safely see fsutil.WriteAtomic.
//   - isNew = false → another row already references the bytes; the file
//     is presumed to exist on disk. The caller can drop
//     any temp file it had pending.
//
// Runs in a single writer transaction so the read-and-update is atomic
// against concurrent inserts of the same sha256 (sqlite serializes
// writers but the read-then-write pattern would race without a tx).
func (s *Store) InsertOrIncBlob(ctx context.Context, sha256 string, size int64, nowUnix int64) (blobID int64, isNew bool, err error) {
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		var existingID int64
		row := tx.QueryRowContext(ctx, `SELECT id FROM blobs WHERE sha256 = ?`, sha256)
		switch err := row.Scan(&existingID); {
		case err == nil:
			// Already present — bump refcount.
			if _, err := tx.ExecContext(ctx,
				`UPDATE blobs SET refcount = refcount + 1 WHERE id = ?`, existingID); err != nil {
				return err
			}
			blobID = existingID
			isNew = false
			return nil
		case errors.Is(err, sql.ErrNoRows):
			// First sighting — insert with refcount = 1.
			res, err := tx.ExecContext(ctx,
				`INSERT INTO blobs(sha256, size_bytes, refcount, created_at) VALUES (?, ?, 1, ?)`,
				sha256, size, nowUnix)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			blobID = id
			isNew = true
			return nil
		default:
			return err
		}
	})
	return
}

// DecBlobRef decrements the refcount of a blob and returns the new value.
// Callers compare against zero to decide whether to schedule the on-disk
// file for deletion. The row itself is NOT removed here — leaving zero-
// refcount rows in place lets the periodic GC reclaim both the file and
// the row in one pass; a power-cut between this call and the GC sweep
// will surface as an orphan blob row that the next GC pass will catch.
func (s *Store) DecBlobRef(ctx context.Context, blobID int64) (refcount int64, err error) {
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE blobs SET refcount = refcount - 1 WHERE id = ?`, blobID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx,
			`SELECT refcount FROM blobs WHERE id = ?`, blobID).Scan(&refcount)
	})
	return
}

// GetBlob returns metadata for a blob by id — used when an attachment is
// opened to translate `attachments.blob_id` into the on-disk path via
// BlobPath. Empty result returns ErrNotFound (matches the rest of the
// storage package's idiom for missing rows).
func (s *Store) GetBlob(ctx context.Context, id int64) (BlobRow, error) {
	var b BlobRow
	row := s.readDB.QueryRowContext(ctx,
		`SELECT id, sha256, size_bytes, refcount, created_at FROM blobs WHERE id = ?`, id)
	if err := row.Scan(&b.ID, &b.SHA256, &b.SizeBytes, &b.Refcount, &b.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BlobRow{}, ErrNotFound
		}
		return BlobRow{}, err
	}
	return b, nil
}

// ListZeroRefBlobs returns every blob whose refcount has dropped to zero
// — these are the GC sweep candidates. The caller is expected to delete
// the on-disk file (BlobPath) AND the `blobs` row in a separate
// transaction; doing both here would couple filesystem and DB I/O in a
// single sqlite tx, which would block writers for the entire sweep.
//
// Listing reads from the read pool, so it doesn't block writes; a
// concurrent InsertOrIncBlob that resurrects a blob between this call
// and the actual GC will simply make the GC's DELETE no-op (the
// refcount > 0 condition in the GC step is the safety net).
func (s *Store) ListZeroRefBlobs(ctx context.Context) ([]BlobRow, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, sha256, size_bytes, refcount, created_at FROM blobs WHERE refcount = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlobRow
	for rows.Next() {
		var b BlobRow
		if err := rows.Scan(&b.ID, &b.SHA256, &b.SizeBytes, &b.Refcount, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SweepBlobs is the GC pass: list every blob with refcount = 0,
// unlink the on-disk file, and (if the unlink succeeded or the file was
// already gone) drop the row. Returns counts so callers can log how
// much disk reclaim happened.
//
// Failure modes:
//   - file unlink fails (permission denied, disk weirdness): the row is
//     left in place and reported in `errors`. The next sweep retries.
//   - row was resurrected by a concurrent download between enumeration
//     and DeleteBlobIfZero: the conditional DELETE refuses to drop it,
//     deletedRows undercounts the candidates, no harm done.
//
// Run on a single goroutine: there's no scheduled-sweep contention to
// guard against, and serializing keeps the slog output coherent.
func (s *Store) SweepBlobs(ctx context.Context, dataDir string) (deletedRows, deletedBytes int64, err error) {
	rows, err := s.ListZeroRefBlobs(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, b := range rows {
		path := BlobPath(dataDir, b.SHA256)
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("blob sweep: unlink failed; row left in place for next pass",
				"blob_id", b.ID, "sha256", b.SHA256, "err", rmErr)
			continue
		}
		dropped, dErr := s.DeleteBlobIfZero(ctx, b.ID)
		if dErr != nil {
			slog.Warn("blob sweep: row delete failed",
				"blob_id", b.ID, "sha256", b.SHA256, "err", dErr)
			continue
		}
		if dropped {
			deletedRows++
			deletedBytes += b.SizeBytes
		}
	}
	return deletedRows, deletedBytes, nil
}

// DeleteBlobIfZero atomically deletes a blob row IF its refcount is
// still zero. A separate function from DecBlobRef so the GC can collect
// candidate ids first (cheap read) and then remove them only after the
// on-disk file has been unlinked successfully — partial failure modes:
//
//   - file unlink fails → blob row stays, GC retries next sweep
//   - file unlink ok, then DELETE fails → orphan row with refcount=0;
//     next sweep notices BlobPath returns ENOENT and deletes the row
//     (caller's responsibility to handle).
//
// Returns true if a row was deleted, false if the refcount has been
// resurrected by a concurrent insert (e.g. a brand-new attachment with
// the same sha256 landed between sweep enumeration and this call).
func (s *Store) DeleteBlobIfZero(ctx context.Context, blobID int64) (bool, error) {
	var deleted bool
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM blobs WHERE id = ? AND refcount = 0`, blobID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		deleted = n > 0
		return nil
	})
	return deleted, err
}
