package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"

	"github.com/spk/spk-mail/internal/fsutil"
	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/pbkdf2"
)

const (
	keyringService = "spk-mail"
	keyringAccount = "master-key"
)

// keyringGet and keyringSet are swappable in tests to avoid touching the OS keyring.
var (
	keyringGet = keyring.Get
	keyringSet = keyring.Set
)

// LoadOrCreateMasterKey looks up the master key in the OS keyring; if missing,
// it generates a fresh 32-byte key and stores it. Returns ErrKeyringUnavailable
// if the keyring service is not reachable so the caller can fall back to a
// password prompt.
func LoadOrCreateMasterKey() ([]byte, error) {
	val, err := keyringGet(keyringService, keyringAccount)
	if err == nil {
		return base64.StdEncoding.DecodeString(val)
	}
	if errors.Is(err, keyring.ErrNotFound) {
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		if err := keyringSet(keyringService, keyringAccount, base64.StdEncoding.EncodeToString(key)); err != nil {
			return nil, err
		}
		return key, nil
	}
	return nil, ErrKeyringUnavailable
}

var ErrKeyringUnavailable = errors.New("secrets: OS keyring unavailable")

// DeriveKeyFromPassword derives a 32-byte key from a user password using
// PBKDF2-HMAC-SHA256, 1_000_000 iterations, with the supplied salt.
// Salt should be 16 random bytes stored alongside secrets.bin.
func DeriveKeyFromPassword(password string, salt []byte) []byte {
	return pbkdf2.Key([]byte(password), salt, 1_000_000, 32, sha256.New)
}

// LoadOrCreateSalt reads a 16-byte PBKDF2 salt from path, creating and
// persisting a fresh one when the file is absent.
func LoadOrCreateSalt(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != 16 {
			return nil, errors.New("secrets: salt file must be 16 bytes")
		}
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	if err := fsutil.AtomicWrite(path, salt, 0o600); err != nil {
		return nil, err
	}
	return salt, nil
}
