package config

import (
	"os"
	"path/filepath"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestConfig_LoadDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadOrDefault(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.Equal(t, "dark", cfg.UI.Theme)
	require.Empty(t, cfg.Accounts)
}

func TestConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	cfg := &Config{
		UI: UIConfig{Theme: "dark"},
		Accounts: []AccountConfig{
			{ID: 1, Name: "Test", Email: "a@b.c", IMAPHost: "imap.x", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", UseMock: false},
		},
	}
	require.NoError(t, cfg.Save(path))
	loaded, err := LoadOrDefault(path)
	require.NoError(t, err)
	require.Equal(t, cfg, loaded)

	// File mode should be 0600 (private)
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}
