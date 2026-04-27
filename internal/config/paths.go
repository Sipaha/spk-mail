package config

import (
	"os"
	"path/filepath"
)

const appName = "spk-mail"

type AppPaths struct {
	ConfigFile     string
	DBFile         string
	SecretsFile    string
	AttachmentsDir string
	LogsDir        string
}

func Paths() (AppPaths, error) {
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	if cfgHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return AppPaths{}, err
		}
		cfgHome = filepath.Join(home, ".config")
	}
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
		ConfigFile:     filepath.Join(cfgHome, appName, "config.yml"),
		DBFile:         filepath.Join(dataDir, "db.sqlite"),
		SecretsFile:    filepath.Join(dataDir, "secrets.bin"),
		AttachmentsDir: filepath.Join(dataDir, "attachments"),
		LogsDir:        filepath.Join(dataDir, "logs"),
	}, nil
}
