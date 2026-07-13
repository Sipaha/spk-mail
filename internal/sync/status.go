package sync

import "sync"

// AccountState is what the UI shows next to an account: the worker's live
// state plus, when it isn't healthy, the reason.
type AccountState struct {
	// State is one of "connecting", "ok", "error".
	State string
	// Detail is the human-readable reason behind a non-ok State — the dial
	// error, typically. Empty when there is nothing to explain.
	Detail string
}

// statusTracker holds the last state each AccountWorker reported.
//
// The worker writes here on the same lines where it emits an AccountStatus
// event, rather than the engine subscribing to the event bus and learning the
// state from it: Emit is deliberately drop-on-full (see api.Emitter), so a busy
// subscriber could miss the "error" event and leave ListAccounts insisting the
// account is fine. The tracker is the source of truth; the event is the
// notification.
type statusTracker struct {
	mu sync.Mutex
	by map[int64]AccountState
}

func newStatusTracker() *statusTracker {
	return &statusTracker{by: map[int64]AccountState{}}
}

func (t *statusTracker) set(id int64, state, detail string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.by[id] = AccountState{State: state, Detail: detail}
}

// get reports the last known state. known is false when no worker has reported
// for this account yet — the caller must NOT read that as healthy.
func (t *statusTracker) get(id int64) (st AccountState, known bool) {
	if t == nil {
		return AccountState{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st, known = t.by[id]
	return st, known
}

func (t *statusTracker) forget(id int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.by, id)
}
