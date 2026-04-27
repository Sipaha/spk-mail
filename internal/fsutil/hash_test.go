package fsutil

import (
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestSHA256Reader(t *testing.T) {
	sum, err := SHA256Reader(strings.NewReader("abc"))
	require.NoError(t, err)
	require.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", sum)
}
