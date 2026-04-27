// Package secrets stores per-account secrets encrypted at rest with AES-256-GCM.
//
// File layout (all big-endian):
//
//	magic    [4]byte  = "SPKM"
//	version  uint8    = 1
//	nonce    [12]byte
//	ciphertext+tag (rest of file)
//
// Plaintext is a JSON map[string][]byte. The whole map is re-encrypted on every Set.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/spk/spk-mail/internal/fsutil"
)

var ErrNotFound = errors.New("secrets: key not found")

const (
	magic   = "SPKM"
	version = byte(1)
)

type Store struct {
	mu   sync.Mutex
	path string
	key  []byte
	data map[string][]byte
}

func Open(path string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: key must be 32 bytes, got %d", len(key))
	}
	s := &Store{path: path, key: key, data: map[string][]byte{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // empty store
	}
	if err != nil {
		return err
	}
	if len(raw) < 4+1+12 {
		return errors.New("secrets: file truncated")
	}
	if string(raw[0:4]) != magic {
		return errors.New("secrets: bad magic")
	}
	if raw[4] != version {
		return fmt.Errorf("secrets: unsupported version %d", raw[4])
	}
	nonce := raw[5:17]
	ct := raw[17:]
	gcm, err := newGCM(s.key)
	if err != nil {
		return err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return fmt.Errorf("secrets: decrypt failed (wrong key?): %w", err)
	}
	return json.Unmarshal(pt, &s.data)
}

func (s *Store) save() error {
	pt, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	gcm, err := newGCM(s.key)
	if err != nil {
		return err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ct := gcm.Seal(nil, nonce, pt, nil)
	out := make([]byte, 0, 4+1+12+len(ct))
	out = append(out, []byte(magic)...)
	out = append(out, version)
	out = append(out, nonce...)
	out = append(out, ct...)
	return fsutil.AtomicWrite(s.path, out, 0o600)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Store) Set(name string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	s.data[name] = cp
	return s.save()
}

func (s *Store) Get(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[name]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, name)
	return s.save()
}
