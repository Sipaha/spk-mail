package fsutil

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func makeFinalPath(root string) func(string) string {
	return func(sha string) string {
		return filepath.Join(root, "blobs", sha[0:2], sha[2:4], sha)
	}
}

// TestWriteContentAddressed_HappyPath: write a payload, get the right
// sha + size + on-disk file at the expected git-style location.
func TestWriteContentAddressed_HappyPath(t *testing.T) {
	root := t.TempDir()
	payload := []byte("hello blob store")
	sha, n, err := WriteContentAddressed(bytes.NewReader(payload), makeFinalPath(root))
	require.NoError(t, err)
	require.EqualValues(t, len(payload), n)
	require.Len(t, sha, 64, "sha must be 64-char lowercase hex")

	got, err := os.ReadFile(makeFinalPath(root)(sha))
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestWriteContentAddressed_DedupHit: writing the same bytes twice must
// land at the same path and leave exactly one file there. The second
// call's temp is silently dropped.
func TestWriteContentAddressed_DedupHit(t *testing.T) {
	root := t.TempDir()
	payload := []byte("dedup me")
	final := makeFinalPath(root)

	sha1, _, err := WriteContentAddressed(bytes.NewReader(payload), final)
	require.NoError(t, err)
	stat1, err := os.Stat(final(sha1))
	require.NoError(t, err)

	sha2, _, err := WriteContentAddressed(bytes.NewReader(payload), final)
	require.NoError(t, err)
	require.Equal(t, sha1, sha2, "same content must hash to same digest")
	stat2, err := os.Stat(final(sha2))
	require.NoError(t, err)

	// On Linux/Darwin, ModTime / inode would differ if the file got
	// replaced. Sys() exposes platform-specific info; we compare
	// ModTime which is stable enough for this guarantee on POSIX
	// (no replace = same mtime).
	require.Equal(t, stat1.ModTime(), stat2.ModTime(),
		"second write must NOT replace the existing file (would invalidate open handles)")
}

// TestWriteContentAddressed_ConcurrentSameContent: many writers, one
// final file. Verifies the rename race doesn't end up with stray temps
// or duplicated final paths. Tests the worst case where all goroutines
// finish hashing within the same scheduling window.
func TestWriteContentAddressed_ConcurrentSameContent(t *testing.T) {
	root := t.TempDir()
	final := makeFinalPath(root)
	payload := []byte("concurrent dedup probe")

	const N = 16
	var wg sync.WaitGroup
	shas := make([]string, N)
	errs := make([]error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			s, _, err := WriteContentAddressed(bytes.NewReader(payload), final)
			shas[i] = s
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		require.NoError(t, e, "goroutine %d", i)
	}
	for i := 1; i < N; i++ {
		require.Equal(t, shas[0], shas[i], "all goroutines must agree on the digest")
	}

	// Walk the blobs/ tree — must contain exactly one file (the final),
	// no leftover .staging-* / .tmp-* artifacts.
	var files []string
	require.NoError(t, filepath.Walk(filepath.Join(root, "blobs"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	}))
	require.Len(t, files, 1, "blobs/ must hold exactly one file across N concurrent identical writes")
}

// TestWriteContentAddressed_DifferentContent: distinct payloads must
// produce distinct paths and never collide.
func TestWriteContentAddressed_DifferentContent(t *testing.T) {
	root := t.TempDir()
	final := makeFinalPath(root)

	sha1, _, err := WriteContentAddressed(bytes.NewReader([]byte("alpha")), final)
	require.NoError(t, err)
	sha2, _, err := WriteContentAddressed(bytes.NewReader([]byte("beta")), final)
	require.NoError(t, err)
	require.NotEqual(t, sha1, sha2)

	_, err = os.Stat(final(sha1))
	require.NoError(t, err)
	_, err = os.Stat(final(sha2))
	require.NoError(t, err)
}
