package events

import (
	"log/slog"
	"sync"
)

type Event struct {
	Type    string         `json:"type"` // MessageArrived|MessageInserted|MessageUpdated|SyncProgress|AccountStatus|FolderMarkedRead
	Payload map[string]any `json:"payload,omitempty"`
}

// subscription owns one subscriber channel. Emit drops e.mu before sending, so
// a send can race Unsubscribe's close(ch) on the same channel — mu serializes
// the two and closed makes the post-close send a no-op instead of a panic.
type subscription struct {
	ch     chan Event
	mu     sync.Mutex
	closed bool
}

// send delivers ev, or drops it if the subscriber's buffer is full. It never
// blocks: Emit runs on the single StoreWriter goroutine that serves every
// account, so waiting on one wedged subscriber (a backgrounded SSE tab, a
// half-open TCP connection whose buffer never drains) would throttle the whole
// write pipeline. A closed subscription drops silently.
func (s *subscription) send(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- ev:
	default:
		slog.Warn("event dropped: subscriber buffer full", "type", ev.Type)
	}
}

// close shuts the channel down. Consumers (tray, the Wails event bridge) exit
// their range loop on the close, so it stays part of the contract.
func (s *subscription) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

type Emitter struct {
	mu   sync.Mutex
	subs []*subscription
}

func NewEmitter() *Emitter { return &Emitter{} }

func (e *Emitter) Subscribe() (<-chan Event, func()) {
	sub := &subscription{ch: make(chan Event, 64)}
	e.mu.Lock()
	e.subs = append(e.subs, sub)
	e.mu.Unlock()
	return sub.ch, func() {
		e.mu.Lock()
		for i, s := range e.subs {
			if s == sub {
				e.subs = append(e.subs[:i], e.subs[i+1:]...)
				break
			}
		}
		e.mu.Unlock()
		// Outside e.mu: close waits behind an in-flight send on this
		// subscription, and must not stall Subscribe/Emit on the whole
		// emitter while it does.
		sub.close()
	}
}

// Emit fans ev out to every subscriber. It holds e.mu across the fan-out: send
// is non-blocking, so the critical section is bounded, and Unsubscribe takes
// sub.mu only AFTER releasing e.mu — there is no lock cycle. Snapshotting the
// slice instead would allocate on every message of a bulk sync.
func (e *Emitter) Emit(ev Event) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sub := range e.subs {
		sub.send(ev)
	}
}

// Emit a typed event without constructing the struct each time. Used by sync writer.
func Emit(em *Emitter, kind string, payload map[string]any) {
	if em == nil {
		return
	}
	em.Emit(Event{Type: kind, Payload: payload})
}
