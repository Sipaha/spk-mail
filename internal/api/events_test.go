package api

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEmitter_DeliversEvent is the happy path: subscribe, emit, receive.
func TestEmitter_DeliversEvent(t *testing.T) {
	e := NewEmitter()
	ch, unsub := e.Subscribe()
	defer unsub()

	e.Emit(Event{Type: "MessageArrived", Payload: map[string]any{"id": float64(1)}})

	select {
	case ev := <-ch:
		require.Equal(t, "MessageArrived", ev.Type)
		require.Equal(t, float64(1), ev.Payload["id"])
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

// TestEmitter_UnsubscribeClosesChannel locks in that the unsubscribe closure
// removes the subscription and closes its channel, so a `for ev := range ch`
// consumer (tray, Wails event bridge) exits cleanly, and that emitting after
// unsubscribe is a safe no-op rather than a panic.
func TestEmitter_UnsubscribeClosesChannel(t *testing.T) {
	e := NewEmitter()
	ch, unsub := e.Subscribe()

	unsub()

	_, ok := <-ch
	require.False(t, ok, "channel should be closed after unsubscribe")

	require.NotPanics(t, func() {
		e.Emit(Event{Type: "after-unsubscribe"})
	})

	// Unsubscribing twice must also be safe (sub.close() is idempotent).
	require.NotPanics(t, unsub)
}

// TestEmitter_FullBufferDropsWithoutStallingHealthySubscribers fills a
// subscriber's 64-slot buffer and then emits one more event. Emit runs on the
// single StoreWriter goroutine serving every account, so it must drop that
// event immediately rather than wait on the wedged subscriber — a wait would
// throttle the whole write pipeline. The dropped event must never land in the
// full subscriber's buffer, and a second, actively-draining subscriber must
// still receive every event promptly.
func TestEmitter_FullBufferDropsWithoutStallingHealthySubscribers(t *testing.T) {
	e := NewEmitter()

	slowCh, unsubSlow := e.Subscribe()
	defer unsubSlow()
	healthyCh, unsubHealthy := e.Subscribe()
	defer unsubHealthy()

	received := make(chan Event, 200)
	go func() {
		for ev := range healthyCh {
			received <- ev
		}
	}()

	// Fill the slow subscriber's buffer (cap 64) without ever draining it.
	// The healthy subscriber drains concurrently so it never fills.
	for i := 0; i < 64; i++ {
		e.Emit(Event{Type: "filler"})
	}

	// Emit a burst against the wedged subscriber. A per-event wait (the old
	// 25ms blocking fallback) would make this take overflow×25ms ≈ 1.25s; a
	// non-blocking drop finishes in microseconds. The bound is generous enough
	// not to flake on a loaded machine while still an order of magnitude below
	// what any per-event wait would cost.
	const overflow = 50
	start := time.Now()
	for i := 0; i < overflow; i++ {
		e.Emit(Event{Type: "overflow"})
	}
	elapsed := time.Since(start)

	require.Lessf(t, elapsed, 300*time.Millisecond,
		"Emit blocked on a full subscriber — it runs on the shared writer goroutine and would throttle the whole write pipeline: %d emits took %s", overflow, elapsed)

	// The healthy subscriber must have received every event.
	require.Eventually(t, func() bool {
		return len(received) == 64+overflow
	}, 2*time.Second, 5*time.Millisecond, "healthy subscriber did not receive all events")

	// The slow subscriber's buffer must hold exactly the 64 filler events —
	// every "overflow" event was dropped, not enqueued.
	count := 0
drain:
	for {
		select {
		case _, ok := <-slowCh:
			if !ok {
				break drain
			}
			count++
		default:
			break drain
		}
	}
	require.Equal(t, 64, count, "slow subscriber's buffer should hold only the filler events")
}

// TestEmitter_ConcurrentSubscribeEmitUnsubscribe is a race regression guard
// for the "send on closed channel" panic: Emit snapshots subscribers under
// e.mu and then calls subscription.send outside it, so a concurrent
// Unsubscribe can close the channel out from under an in-flight send unless
// subscription.mu / closed correctly serialize the two. Run with -race.
func TestEmitter_ConcurrentSubscribeEmitUnsubscribe(_ *testing.T) {
	e := NewEmitter()

	const goroutines = 12
	const iterations = 40

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				ch, unsub := e.Subscribe()
				drained := make(chan struct{})
				go func() {
					defer close(drained)
					// Drain until unsub closes the channel; the values
					// themselves don't matter, only that no send panics.
					for ev := range ch {
						_ = ev
					}
				}()
				e.Emit(Event{Type: "concurrent"})
				unsub()
				<-drained
			}
		}()
	}

	// Hammer Emit concurrently with the Subscribe/unsub churn above so a
	// snapshot taken mid-churn is exercised too.
	stop := make(chan struct{})
	var emitWG sync.WaitGroup
	emitWG.Add(1)
	go func() {
		defer emitWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				e.Emit(Event{Type: "background"})
			}
		}
	}()

	wg.Wait()
	close(stop)
	emitWG.Wait()
}
