package storage

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestThreads_InsertAndList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id, err := s.InsertThread(ctx, ThreadRow{SubjectNorm: "Hello", LastDate: 1700000000, MsgCount: 1})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	rows, err := s.ListThreadsRecent(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Hello", rows[0].SubjectNorm)
}
