package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spk/spk-mail/internal/fsutil"
	"github.com/spk/spk-mail/internal/imapconn"
	"github.com/spk/spk-mail/internal/storage"
)

// ErrRawUnavailable signals that raw bytes can't be obtained — the
// message's IMAP UID is no longer present on the server (deleted from
// another client, or moved to a folder we don't track). Distinct from
// generic transport errors so the frontend can render a focused
// message instead of "internal error".
var ErrRawUnavailable = errors.New("raw RFC822 unavailable for this message")

// rawFilename builds the .eml filename used for download. Priority:
//
//  1. subject sanitized + truncated to 80 runes
//  2. message-id (without surrounding < >) sanitized
//  3. "message-<msgID>.eml" as a last resort
//
// Sanitization replaces filesystem-hostile characters with "_", trims
// trailing dots and whitespace, and caps at 80 runes (UTF-8 aware).
func rawFilename(subject *string, messageID *string, msgID int64) string {
	if name := cleanCandidate(derefSafe(subject)); name != "" {
		return name + ".eml"
	}
	if mid := derefSafe(messageID); mid != "" {
		mid = strings.TrimPrefix(mid, "<")
		mid = strings.TrimSuffix(mid, ">")
		if name := cleanCandidate(mid); name != "" {
			return name + ".eml"
		}
	}
	return fmt.Sprintf("message-%d.eml", msgID)
}

func derefSafe(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var fnameSanitizer = regexp.MustCompile(`[\\/<>:"|?*\x00-\x1f]`)

func cleanCandidate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = fnameSanitizer.ReplaceAllString(s, "_")
	s = strings.TrimRight(s, ". ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	return string(runes)
}

// GetRawMessage returns the RFC822 source of a message, fetching from
// IMAP and caching in the blob store on first call. Resolution order:
//
//  1. Cached blob with on-disk file — return immediately.
//  2. Cached blob with missing file — clear link, DecBlobRef, fall
//     through to step 3.
//  3. Lazy IMAP fetch — Select the message's folder, FETCH by UID,
//     write to blob store, link.
//
// IMAP returning zero matches surfaces as ErrRawUnavailable so the
// frontend can render a focused message instead of "internal error".
func (s *Stub) GetRawMessage(ctx context.Context, id int64) (RawMessageDTO, error) {
	msg, err := s.Store.GetMessage(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return RawMessageDTO{}, ErrRawUnavailable
		}
		return RawMessageDTO{}, err
	}

	if raw, ok, err := s.tryCached(ctx, id); err != nil {
		return RawMessageDTO{}, err
	} else if ok {
		return s.buildDTO(raw, msg, id), nil
	}

	raw, err := s.fetchAndStoreRaw(ctx, msg, id)
	if err != nil {
		return RawMessageDTO{}, err
	}
	return s.buildDTO(raw, msg, id), nil
}

// tryCached returns (bytes, true, nil) when the cached blob exists on
// disk. Returns (nil, false, nil) when the slot is NULL or the file
// vanished (in which case the link is cleared + refcount decremented
// before returning, so the caller can fall through to lazy fetch).
func (s *Stub) tryCached(ctx context.Context, msgID int64) ([]byte, bool, error) {
	if s.DataDir == "" {
		return nil, false, nil
	}
	_, sha, found, err := s.Store.GetMessageRawBlob(ctx, msgID)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	path := storage.BlobPath(s.DataDir, sha)
	data, err := os.ReadFile(path)
	if err == nil {
		return data, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		// Real I/O error (permission denied, EIO, etc.) — don't destroy
		// the cache link, surface the error so the caller knows.
		return nil, false, err
	}
	if prev, cErr := s.Store.ClearMessageRawBlob(ctx, msgID); cErr == nil && prev != nil {
		if _, dErr := s.Store.DecBlobRef(ctx, *prev); dErr != nil {
			slog.Warn("raw: DecBlobRef after missing file", "err", dErr)
		}
	}
	return nil, false, nil
}

// fetchAndStoreRaw opens an IMAP session, fetches the raw bytes by
// UID, persists them via the content-addressed blob store, and links
// the message row. Returns the raw bytes.
func (s *Stub) fetchAndStoreRaw(ctx context.Context, msg storage.MessageRow, msgID int64) ([]byte, error) {
	folders, err := s.Store.ListFolders(ctx, msg.AccountID)
	if err != nil {
		return nil, err
	}
	folderName := ""
	for _, f := range folders {
		if f.ID == msg.FolderID {
			folderName = f.Name
			break
		}
	}
	if folderName == "" {
		return nil, ErrRawUnavailable
	}

	c, err := imapconn.DialAccount(ctx, s.Store, s.Secrets, msg.AccountID)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if _, err := c.Select(ctx, folderName); err != nil {
		return nil, err
	}

	out, errCh := c.FetchByUIDs(ctx, []int64{msg.UID})
	var raw []byte
	for fm := range out {
		if len(fm.Raw) > 0 && raw == nil {
			raw = fm.Raw
		}
	}
	if err := <-errCh; err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrRawUnavailable
	}

	if s.DataDir != "" {
		s.persistRaw(ctx, msgID, raw)
	}
	return raw, nil
}

// persistRaw writes raw bytes to the content-addressed blob store and
// links them to the message. Best-effort: errors are logged but do
// not fail the user-facing GetRawMessage call.
func (s *Stub) persistRaw(ctx context.Context, msgID int64, raw []byte) {
	sha, size, err := fsutil.WriteContentAddressed(bytes.NewReader(raw), func(hex string) string {
		return storage.BlobPath(s.DataDir, hex)
	})
	if err != nil {
		slog.Warn("raw: WriteContentAddressed failed", "err", err)
		return
	}
	blobID, _, err := s.Store.InsertOrIncBlob(ctx, sha, size, time.Now().Unix())
	if err != nil {
		slog.Warn("raw: InsertOrIncBlob failed", "err", err)
		return
	}
	res, prev, err := s.Store.SetMessageRawBlob(ctx, msgID, blobID, time.Now().Unix())
	if err != nil {
		slog.Warn("raw: SetMessageRawBlob failed", "err", err)
		_, _ = s.Store.DecBlobRef(ctx, blobID)
		return
	}
	switch res {
	case storage.SetReplaced:
		_, _ = s.Store.DecBlobRef(ctx, prev)
	case storage.SetNoop:
		_, _ = s.Store.DecBlobRef(ctx, blobID)
	}
}

func (s *Stub) buildDTO(raw []byte, msg storage.MessageRow, msgID int64) RawMessageDTO {
	return RawMessageDTO{
		Filename:  rawFilename(msg.Subject, msg.MessageID, msgID),
		SizeBytes: int64(len(raw)),
		RawB64:    base64.StdEncoding.EncodeToString(raw),
	}
}
