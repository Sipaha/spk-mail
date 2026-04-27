//go:build wails

package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// PromptMasterPassword's only currently-testable behavior is the salt
// generation/persistence path before the (stubbed) password window is shown.
func TestPromptMasterPassword_GeneratesAndPersistsSalt(t *testing.T) {
	dir := t.TempDir()
	secretsPath := filepath.Join(dir, "secrets.bin")

	// First call: no salt file exists; PromptMasterPassword will fail at
	// openPasswordWindow (stub) but should have created master.salt first.
	_, err := PromptMasterPassword(nil, secretsPath)
	require.Error(t, err) // expected: stub error from openPasswordWindow

	saltBytes, err := os.ReadFile(filepath.Join(dir, "master.salt"))
	require.NoError(t, err)
	require.Len(t, saltBytes, 16)

	// Second call: salt file is reused, not regenerated.
	_, _ = PromptMasterPassword(nil, secretsPath)
	saltBytes2, err := os.ReadFile(filepath.Join(dir, "master.salt"))
	require.NoError(t, err)
	require.Equal(t, saltBytes, saltBytes2)
}
