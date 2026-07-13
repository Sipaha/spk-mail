package sync

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/stretchr/testify/require"
)

// TestEngine_SuperviseBackoff verifies the supervise loop waits at least the
// first-tier delay (1s) before restarting a worker that exits immediately.
func TestEngine_SuperviseBackoff(t *testing.T) {
	var runs atomic.Int32

	fx := setupMockAccount(t, "alice@example.com", "secret")
	em := api.NewEmitter()
	e := NewEngine(fx.Store, fx.Secrets, em)
	e.runWorker = func(_ *AccountWorker, _ context.Context) {
		runs.Add(1)
	}
	w := NewAccountWorker(fx.AccID, fx.Store, fx.Secrets, nil, em)

	e.mu.Lock()
	e.workers[fx.AccID] = w
	e.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	start := time.Now()
	done := make(chan struct{})
	go func() {
		e.supervise(ctx, fx.AccID, w)
		close(done)
	}()

	require.Eventually(t, func() bool { return runs.Load() >= 2 }, 3*time.Second, 20*time.Millisecond)
	require.GreaterOrEqual(t, time.Since(start), 900*time.Millisecond,
		"supervise must apply the 1s first-tier backoff before the second run")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervise did not exit after ctx cancel")
	}
}
