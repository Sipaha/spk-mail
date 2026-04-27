package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/pbkdf2"
)

const (
	keyringService = "spk-mail"
	keyringAccount = "master-key"
)

// LoadOrCreateMasterKey looks up the master key in the OS keyring; if missing,
// it generates a fresh 32-byte key and stores it. Returns ErrKeyringUnavailable
// if the keyring service is not reachable so the caller can fall back to a
// password prompt.
func LoadOrCreateMasterKey() ([]byte, error) {
	val, err := keyring.Get(keyringService, keyringAccount)
	if err == nil {
		return base64.StdEncoding.DecodeString(val)
	}
	if errors.Is(err, keyring.ErrNotFound) {
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		if err := keyring.Set(keyringService, keyringAccount, base64.StdEncoding.EncodeToString(key)); err != nil {
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
