package tray

import "sync"

// ActionContext is everything the action-invoked handler needs to navigate
// the app to the right place. Stored against the notification id returned
// by org.freedesktop.Notifications.Notify and looked up when the daemon
// emits ActionInvoked for that id.
type ActionContext struct {
	AccountID int64
	ThreadID  int64
	MessageID int64
}

// pendingActions is a small, bounded notification-id → ActionContext map.
//
// It's bounded because some notification daemons never emit
// NotificationClosed for dismissed-by-timeout notifications (older
// xfce4-notifyd, some Plasma versions), so without a cap the map would
// grow unbounded across long sessions. A cap of 256 covers the realistic
// burst (a few minutes of heavy mail) while staying tiny in memory.
type pendingActions struct {
	mu    sync.Mutex
	cap   int
	order []uint32 // insertion order, used to evict the oldest on overflow
	by    map[uint32]ActionContext
}

func newPendingActions(capacity int) *pendingActions {
	if capacity <= 0 {
		capacity = 256
	}
	return &pendingActions{
		cap:   capacity,
		order: make([]uint32, 0, capacity),
		by:    make(map[uint32]ActionContext, capacity),
	}
}

// Put records ctx for id. If id already exists, its context is replaced
// in place (keeping its position in the eviction order). When the map
// reaches capacity, the oldest entry is evicted before insertion.
func (p *pendingActions) Put(id uint32, ctx ActionContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.by[id]; ok {
		p.by[id] = ctx
		return
	}
	if len(p.order) >= p.cap {
		oldest := p.order[0]
		p.order = p.order[1:]
		delete(p.by, oldest)
	}
	p.order = append(p.order, id)
	p.by[id] = ctx
}

// Take returns ctx for id and removes it. ok=false when id is unknown
// (e.g. a notification not posted by us, or one already taken by an
// earlier signal).
func (p *pendingActions) Take(id uint32) (ActionContext, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, ok := p.by[id]
	if !ok {
		return ActionContext{}, false
	}
	delete(p.by, id)
	for i, x := range p.order {
		if x == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	return ctx, true
}

// Delete drops id without returning the context. Used on
// NotificationClosed signals so dismissed notifications don't linger
// and pin the eviction queue.
func (p *pendingActions) Delete(id uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.by[id]; !ok {
		return
	}
	delete(p.by, id)
	for i, x := range p.order {
		if x == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
}

// Len reports the current number of tracked notifications. Test-only;
// the controller never inspects size.
func (p *pendingActions) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.by)
}
