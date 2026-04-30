package config

import (
	"os"
	"path/filepath"
)

const appName = "spk-mail"

type AppPaths struct {
	DBFile         string
	SecretsFile    string
	AttachmentsDir string // legacy: pre-v7 per-message file tree; kept for migration v8
	DataDir        string // root the content-addressed blob store lives under (<DataDir>/blobs/...)
}

func Paths() (AppPaths, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return AppPaths{}, err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	dataDir := filepath.Join(dataHome, appName)
	return AppPaths{
		DBFile:         filepath.Join(dataDir, "db.sqlite"),
		SecretsFile:    filepath.Join(dataDir, "secrets.bin"),
		AttachmentsDir: filepath.Join(dataDir, "attachments"),
		DataDir:        dataDir,
	}, nil
}
