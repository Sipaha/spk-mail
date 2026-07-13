package secrets

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestLoadOrCreateMasterKey_Existing(t *testing.T) {
	oldGet, oldSet := keyringGet, keyringSet
	t.Cleanup(func() { keyringGet, keyringSet = oldGet, oldSet })

	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(want)
	keyringGet = func(service, account string) (string, error) {
		require.Equal(t, keyringService, service)
		require.Equal(t, keyringAccount, account)
		return encoded, nil
	}
	keyringSet = func(string, string, string) error {
		t.Fatal("Set must not be called when key already exists")
		return nil
	}

	got, err := LoadOrCreateMasterKey()
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestLoadOrCreateMasterKey_CreatesWhenMissing(t *testing.T) {
	oldGet, oldSet := keyringGet, keyringSet
	t.Cleanup(func() { keyringGet, keyringSet = oldGet, oldSet })

	var stored string
	keyringGet = func(string, string) (string, error) {
		return "", keyring.ErrNotFound
	}
	keyringSet = func(service, account, val string) error {
		require.Equal(t, keyringService, service)
		require.Equal(t, keyringAccount, account)
		stored = val
		return nil
	}

	got, err := LoadOrCreateMasterKey()
	require.NoError(t, err)
	require.Len(t, got, 32)
	require.Equal(t, base64.StdEncoding.EncodeToString(got), stored)
}

func TestLoadOrCreateMasterKey_KeyringUnavailable(t *testing.T) {
	oldGet, oldSet := keyringGet, keyringSet
	t.Cleanup(func() { keyringGet, keyringSet = oldGet, oldSet })

	keyringGet = func(string, string) (string, error) {
		return "", errors.New("dbus offline")
	}
	keyringSet = func(string, string, string) error {
		t.Fatal("Set must not be called when keyring is unavailable")
		return nil
	}

	_, err := LoadOrCreateMasterKey()
	require.ErrorIs(t, err, ErrKeyringUnavailable)
}
