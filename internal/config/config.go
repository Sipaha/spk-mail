package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	UI       UIConfig        `yaml:"ui"`
	Accounts []AccountConfig `yaml:"accounts"`
}

type UIConfig struct {
	Theme string `yaml:"theme"` // "dark" only for now
}

type AccountConfig struct {
	ID           int64  `yaml:"id"`
	Name         string `yaml:"name"`
	Email        string `yaml:"email"`
	IMAPHost     string `yaml:"imap_host"`
	IMAPPort     int    `yaml:"imap_port"`
	IMAPUsername string `yaml:"imap_username"`
	UseTLS       bool   `yaml:"use_tls"`
	Color        string `yaml:"color"`
	UseMock      bool   `yaml:"use_mock,omitempty"` // dev/test: connect to in-process mock
}

func LoadOrDefault(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.UI.Theme == "" {
		cfg.UI.Theme = "dark"
	}
	return &cfg, nil
}

func defaultConfig() *Config {
	return &Config{UI: UIConfig{Theme: "dark"}}
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

