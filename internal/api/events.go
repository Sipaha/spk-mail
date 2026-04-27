package api

import "sync"

type Event struct {
	Type    string         `json:"type"` // MessageArrived|MessageInserted|MessageUpdated|SyncProgress|AccountStatus
	Payload map[string]any `json:"payload,omitempty"`
}

type Emitter struct {
	mu   sync.Mutex
	subs []chan Event
}

func NewEmitter() *Emitter { return &Emitter{} }

func (e *Emitter) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	e.mu.Lock()
	e.subs = append(e.subs, ch)
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		for i, c := range e.subs {
			if c == ch {
				e.subs = append(e.subs[:i], e.subs[i+1:]...)
				close(c)
				return
			}
		}
	}
}

func (e *Emitter) Emit(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ch := range e.subs {
		select {
		case ch <- ev:
		default: // drop on slow subscriber
		}
	}
}
