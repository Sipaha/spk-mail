package fsutil

import (
	"io"
	"os"
	"path/filepath"
)

// WriteContentAddressed streams `r` into a temp file in the destination
// directory while computing the sha256 of its bytes, then renames the
// temp into final position. Returns the lowercase-hex digest and the
// number of bytes written.
//
// The caller passes a `finalPath` callback that receives the digest and
// returns where the file should land — this lets the caller use the same
// path scheme as storage.BlobPath without leaking layout knowledge into
// fsutil.
//
// Concurrency: two callers writing the SAME content (same digest) end
// up racing on os.Rename. POSIX rename is atomic — exactly one of them
// "wins" and produces a single file at the final path. The losing
// caller sees its temp file consumed by rename, which is harmless: the
// destination is byte-identical so neither caller cares which write
// committed.
//
// If the final path already exists with a populated file (the common
// case in dedup hits), we DON'T overwrite it: rename would replace the
// inode and break any open file handles. Instead the temp is unlinked
// and the existing file is left in place. The sha returned is computed
// from the input regardless, so the caller can verify it matches what
// storage.InsertOrIncBlob expected.
//
// Best-effort fsync of both the file and the parent directory keeps the
// data + the rename durable across a power cut.
func WriteContentAddressed(r io.Reader, finalPath func(sha string) string) (sha string, size int64, err error) {
	// Stage in a system temp dir first — we only know the destination
	// after we've consumed the whole reader. Using the system temp
	// avoids polluting the blobs tree with .tmp-* files when the
	// dest sub-directory doesn't exist yet.
	tmp, err := os.CreateTemp("", "spk-blob-*")
	if err != nil {
		return "", 0, err
	}
	tmpName := tmp.Name()
	// Deferred cleanup: if we never rename successfully, the temp must
	// be removed. The os.Remove after a successful Rename is a no-op
	// (the tmp file no longer exists at that path).
	defer func() { _ = os.Remove(tmpName) }()

	sha, n, err := sha256Copy(tmp, r)
	if err != nil {
		_ = tmp.Close()
		return "", 0, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}

	dest := finalPath(sha)

	if _, statErr := os.Stat(dest); statErr == nil {
		// Dedup hit: file is already there. Drop the temp (deferred
		// Remove handles it) and return success — the caller's
		// InsertOrIncBlob will still record the new attachment ref.
		return sha, n, nil
	} else if !os.IsNotExist(statErr) {
		return "", 0, statErr
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", 0, err
	}
	// On Linux a rename across the same filesystem is atomic. The
	// temp lives in the system temp dir which may be on a different
	// fs (tmpfs), in which case Rename returns EXDEV and we fall back
	// to copy + remove. The copy still keeps the destination atomic
	// against readers because we write into <dest>.staging-* in the
	// destination directory and rename from there.
	if err := os.Rename(tmpName, dest); err != nil {
		if !isCrossDevice(err) {
			return "", 0, err
		}
		if err := copyAndRename(tmpName, dest); err != nil {
			return "", 0, err
		}
	}

	// Blobs are raw message/attachment bytes; keep them owner-only. CreateTemp
	// already makes the staged file 0600 and rename preserves the mode, so this
	// is defensive — it pins the guarantee even if the staging path changes.
	_ = os.Chmod(dest, 0o600)

	// fsync the parent directory so the rename is durable. Best-effort:
	// some filesystems return EINVAL on dir-fsync (notably 9p / virtio
	// passthrough), in which case we accept the lesser durability
	// rather than failing the whole write.
	if dir, err := os.Open(filepath.Dir(dest)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return sha, n, nil
}

// copyAndRename handles the EXDEV fallback: read the temp from its
// (foreign) filesystem and write it into a local staging file inside
// the destination directory, then rename to the final path. Bytes are
// re-streamed but no extra hash work — the caller already has the
// digest from the first pass.
func copyAndRename(srcPath, destPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	staging, err := os.CreateTemp(filepath.Dir(destPath), ".staging-*")
	if err != nil {
		return err
	}
	stagingName := staging.Name()
	defer func() { _ = os.Remove(stagingName) }()
	if _, err := io.Copy(staging, src); err != nil {
		_ = staging.Close()
		return err
	}
	if err := staging.Sync(); err != nil {
		_ = staging.Close()
		return err
	}
	if err := staging.Close(); err != nil {
		return err
	}
	return os.Rename(stagingName, destPath)
}
