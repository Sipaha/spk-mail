package config

import (
	"os"
	"path/filepath"
)

const appName = "spk-mail"

type AppPaths struct {
	DBFile      string
	SecretsFile string
	DataDir     string // root the content-addressed blob store lives under (<DataDir>/blobs/...)
}

// Paths returns on-disk locations for spk-mail state. By default everything
// lives under ~/.spk/spk-mail/ so it sits alongside other SPK products
// (editor, cockpit). XDG_DATA_HOME still overrides the root for test isolation
// — when set, paths resolve to $XDG_DATA_HOME/spk-mail/.
func Paths() (AppPaths, error) {
	var dataDir string
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		dataDir = filepath.Join(dataHome, appName)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return AppPaths{}, err
		}
		dataDir = filepath.Join(home, ".spk", appName)
	}
	return AppPaths{
		DBFile:      filepath.Join(dataDir, "db.sqlite"),
		SecretsFile: filepath.Join(dataDir, "secrets.bin"),
		DataDir:     dataDir,
	}, nil
}
