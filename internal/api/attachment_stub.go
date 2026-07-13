package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spk/spk-mail/internal/storage"
)

// GetAttachmentLocalPath returns the local filesystem path for an
// attachment that's already been downloaded. Resolution priority:
//
//  1. blob_id is set → compose storage.BlobPath(DataDir, sha256). This
//     is the post-v7 path; multiple attachments may resolve to the
//     SAME on-disk file due to content-addressed dedup.
//  2. fall back to legacy local_path (pre-v7 rows; populated until
//     migration v8 backfills them into blobs).
//
// If the resolved file is missing, the row's blob link is cleared
// (and the blob refcount decremented) so the downloader re-fetches
// the bytes on the next sweep, and the caller sees
// ErrAttachmentNotReady. A nonexistent attachment id maps to the same
// sentinel — surfacing sql.ErrNoRows verbatim would leak "sql: no rows
// in result set" into the HTTP response body the user's browser sees.
func (s *Stub) GetAttachmentLocalPath(ctx context.Context, id int64) (string, error) {
	if s.DataDir != "" {
		blobID, sha, found, err := s.Store.GetAttachmentBlob(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrAttachmentNotReady
		}
		if err != nil {
			return "", err
		}
		if found {
			path := storage.BlobPath(s.DataDir, sha)
			if _, err := os.Stat(path); err != nil {
				// File missing on disk — clear blob link + dec the
				// blob's refcount so the downloader re-fetches and
				// any orphan blob row gets GC'd on the next sweep.
				if prevID, err := s.Store.ClearAttachmentBlob(ctx, id); err == nil && prevID != nil {
					if _, derr := s.Store.DecBlobRef(ctx, *prevID); derr != nil {
						// Best-effort: log, continue. The next sweep
						// will reconcile from refcount.
						_ = derr
					}
				}
				_ = blobID
				return "", ErrAttachmentNotReady
			}
			return path, nil
		}
	}

	// Pre-v7 row OR DataDir not configured (test mode): fall back.
	path, found, err := s.Store.GetAttachmentLocalPath(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAttachmentNotReady
	}
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrAttachmentNotReady
	}
	if _, err := os.Stat(path); err != nil {
		_, _ = s.Store.ClearAttachmentBlob(ctx, id)
		return "", ErrAttachmentNotReady
	}
	if s.DataDir != "" {
		if err := ensurePathUnderDataDir(s.DataDir, path); err != nil {
			return "", err
		}
	}
	return path, nil
}

func ensurePathUnderDataDir(dataDir, path string) error {
	absRoot, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve attachment path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("attachment path outside data directory")
	}
	return nil
}

// OpenAttachment hands the local file off to xdg-open (Linux). Detached: we
// don't wait for the opener to finish. Uses context.Background() for the exec
// so that a short-lived API request ctx doesn't SIGKILL xdg-open before it
// forks the real viewer.
func (s *Stub) OpenAttachment(ctx context.Context, id int64) error {
	path, err := s.GetAttachmentLocalPath(ctx, id)
	if err != nil {
		return err
	}
	cmd := exec.Command("xdg-open", path)
	return cmd.Start()
}
