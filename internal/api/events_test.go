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

// TestEmitter_FullBufferDropsWithoutStallingHealthySubscribers wedges one
// subscriber (never drained, buffer fills at 64) and keeps a second one healthy.
// Emit runs on the single StoreWriter goroutine serving every account, so it
// must drop for the wedged subscriber instead of waiting on it — a wait would
// throttle the whole write pipeline. The healthy subscriber is drained after
// every Emit, so it can never back up: with drop-on-full semantics that is the
// only way to assert "a wedged peer costs a healthy one nothing" without racing
// the scheduler.
func TestEmitter_FullBufferDropsWithoutStallingHealthySubscribers(t *testing.T) {
	e := NewEmitter()

	slowCh, unsubSlow := e.Subscribe()
	defer unsubSlow()
	healthyCh, unsubHealthy := e.Subscribe()
	defer unsubHealthy()

	healthy := 0
	emit := func(kind string) {
		e.Emit(Event{Type: kind})
		select {
		case <-healthyCh:
			healthy++
		default:
		}
	}

	// Fill the wedged subscriber's buffer (cap 64).
	for i := 0; i < 64; i++ {
		emit("filler")
	}

	// Burst against the now-full subscriber. The old blocking fallback waited
	// 25ms per event, so this would take overflow×25ms ≈ 1.25s; dropping takes
	// microseconds. The bound is generous enough not to flake on a loaded
	// machine and still an order of magnitude below any per-event wait.
	const overflow = 50
	start := time.Now()
	for i := 0; i < overflow; i++ {
		emit("overflow")
	}
	elapsed := time.Since(start)

	require.Lessf(t, elapsed, 300*time.Millisecond,
		"Emit blocked on a wedged subscriber — it runs on the shared writer goroutine and would throttle the whole write pipeline: %d emits took %s", overflow, elapsed)

	// The healthy subscriber saw every event: it never backed up, so nothing of
	// its own was dropped, and the wedged peer did not starve it.
	require.Equal(t, 64+overflow, healthy, "healthy subscriber missed events")

	// The wedged subscriber's buffer holds exactly the 64 fillers — every
	// "overflow" event was dropped, not enqueued.
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
	require.Equal(t, 64, count, "wedged subscriber's buffer should hold only the filler events")
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
