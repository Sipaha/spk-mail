package testapi

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSlogHandler_RecordsToBuffer(t *testing.T) {
	rb := NewRingBuffer(8)
	inner := slog.NewTextHandler(os.Stderr, nil)
	logger := slog.New(NewSlogHandler(inner, rb))
	logger.InfoContext(context.Background(), "hello")
	logger.WarnContext(context.Background(), "watch out")
	snap := rb.Snapshot()
	require.Len(t, snap, 2)
	require.Equal(t, "hello", snap[0].Message)
	require.Equal(t, "WARN", snap[1].Level)
}
