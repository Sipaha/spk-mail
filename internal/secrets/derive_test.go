package secrets

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveKeyFromPassword_Deterministic(t *testing.T) {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	a := DeriveKeyFromPassword("hunter2", salt)
	b := DeriveKeyFromPassword("hunter2", salt)
	require.Equal(t, a, b)
	require.Len(t, a, 32)
}

func TestDeriveKeyFromPassword_DiffersBySalt(t *testing.T) {
	s1 := make([]byte, 16)
	_, _ = rand.Read(s1)
	s2 := make([]byte, 16)
	_, _ = rand.Read(s2)
	require.NotEqual(t, DeriveKeyFromPassword("pw", s1), DeriveKeyFromPassword("pw", s2))
}

func TestDeriveKeyFromPassword_DiffersByPassword(t *testing.T) {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	require.NotEqual(t, DeriveKeyFromPassword("a", salt), DeriveKeyFromPassword("b", salt))
}
