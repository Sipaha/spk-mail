package api

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
)

// GetAttachmentLocalPath returns the local filesystem path for an attachment
// that's already been downloaded. If the row has no local_path or the file is
// missing, it returns ErrAttachmentNotReady (after clearing a stale path so
// the downloader will re-fetch). A nonexistent attachment id maps to the same
// sentinel — surfacing sql.ErrNoRows verbatim would leak "sql: no rows in
// result set" into the HTTP response body the user's browser sees.
func (s *Stub) GetAttachmentLocalPath(ctx context.Context, id int64) (string, error) {
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
		// File missing — clear so the downloader will re-fetch.
		_ = s.Store.ClearAttachmentLocalPath(ctx, id)
		return "", ErrAttachmentNotReady
	}
	return path, nil
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
