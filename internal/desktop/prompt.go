//go:build wails

package desktop

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mail/internal/fsutil"
	"github.com/spk/spk-mail/internal/secrets"
)

// PromptMasterPassword opens a small modal asking for a password and returns
// a derived 32-byte key. Persists a per-installation 16-byte salt at
// <secretsDir>/master.salt; reuses it on subsequent runs.
func PromptMasterPassword(app *application.App, secretsPath string) ([]byte, error) {
	saltPath := filepath.Join(filepath.Dir(secretsPath), "master.salt")
	salt, err := os.ReadFile(saltPath)
	if errors.Is(err, os.ErrNotExist) {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("generate salt: %w", err)
		}
		if err := fsutil.AtomicWrite(saltPath, salt, 0o600); err != nil {
			return nil, fmt.Errorf("write salt: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read salt: %w", err)
	}

	pw, err := openPasswordWindow(app)
	if err != nil {
		return nil, err
	}
	if pw == "" {
		return nil, errors.New("no password entered")
	}
	return secrets.DeriveKeyFromPassword(pw, salt), nil
}

// openPasswordWindow opens a Wails window with a password input and returns
// the entered string (or "" if cancelled). Wails v3 alpha.78 does not expose
// a built-in password dialog, so this is a TODO until the real UI is wired
// in a follow-up plan. PromptMasterPassword is callable as scaffolding but
// will return this error until the UI is implemented.
func openPasswordWindow(app *application.App) (string, error) {
	return "", errors.New("password prompt window not yet implemented")
}
