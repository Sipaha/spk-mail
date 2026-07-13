package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteContentAddressed_BlobIsOwnerOnly pins that a written blob is 0600 —
// blobs are raw message/attachment bytes and must not be group/world readable.
func TestWriteContentAddressed_BlobIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	final := func(sha string) string { return filepath.Join(dir, sha[:2], sha) }

	sha, _, err := WriteContentAddressed(strings.NewReader("secret body"), final)
	require.NoError(t, err)

	fi, err := os.Stat(final(sha))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "blob must be owner-only (0600)")
}
