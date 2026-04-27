package secrets

import (
	"path/filepath"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.bin")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	s, err := Open(path, key)
	require.NoError(t, err)

	require.NoError(t, s.Set("account:1", []byte("hunter2")))
	require.NoError(t, s.Set("account:2", []byte("supersecret")))

	got, err := s.Get("account:1")
	require.NoError(t, err)
	require.Equal(t, "hunter2", string(got))

	// Reopen with same key
	s2, err := Open(path, key)
	require.NoError(t, err)
	got2, err := s2.Get("account:2")
	require.NoError(t, err)
	require.Equal(t, "supersecret", string(got2))
}

func TestStore_WrongKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.bin")
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	keyB[0] = 1

	s, err := Open(path, keyA)
	require.NoError(t, err)
	require.NoError(t, s.Set("k", []byte("v")))

	_, err = Open(path, keyB)
	require.Error(t, err)
}

func TestStore_DeleteKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.bin")
	key := make([]byte, 32)
	s, _ := Open(path, key)
	_ = s.Set("k", []byte("v"))
	require.NoError(t, s.Delete("k"))
	_, err := s.Get("k")
	require.ErrorIs(t, err, ErrNotFound)
}
