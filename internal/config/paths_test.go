package config

import (
	"path/filepath"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestPaths_RespectsEnvOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/spk-cfg")
	t.Setenv("XDG_DATA_HOME", "/tmp/spk-data")
	p, err := Paths()
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/tmp/spk-cfg", "spk-mail", "config.yml"), p.ConfigFile)
	require.Equal(t, filepath.Join("/tmp/spk-data", "spk-mail", "db.sqlite"), p.DBFile)
	require.Equal(t, filepath.Join("/tmp/spk-data", "spk-mail", "secrets.bin"), p.SecretsFile)
	require.Equal(t, filepath.Join("/tmp/spk-data", "spk-mail", "attachments"), p.AttachmentsDir)
	require.Equal(t, filepath.Join("/tmp/spk-data", "spk-mail", "logs"), p.LogsDir)
}

func TestPaths_DefaultsWhenUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/test")
	p, err := Paths()
	require.NoError(t, err)
	require.Equal(t, "/home/test/.config/spk-mail/config.yml", p.ConfigFile)
	require.Equal(t, "/home/test/.local/share/spk-mail/db.sqlite", p.DBFile)
}
