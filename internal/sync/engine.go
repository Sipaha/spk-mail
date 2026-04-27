package sync

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// Engine is a process-wide supervisor for AccountWorkers. It owns a single
// StoreWriter and one AccountWorker per active account, restarting crashed
// workers with exponential backoff.
type Engine struct {
	store   *storage.Store
	secrets *secrets.Store
	em      *api.Emitter

	mu      sync.Mutex
	workers map[int64]*AccountWorker
	cancels map[int64]context.CancelFunc
	writer  *StoreWriter
}

// NewEngine constructs an Engine. It performs no I/O.
func NewEngine(s *storage.Store, sec *secrets.Store, em *api.Emitter) *Engine {
	return &Engine{
		store:   s,
		secrets: sec,
		em:      em,
		workers: map[int64]*AccountWorker{},
		cancels: map[int64]context.CancelFunc{},
	}
}

// Run starts the StoreWriter and a worker per account currently in the DB.
// It blocks until ctx is cancelled, then signals all workers to stop.
func (e *Engine) Run(ctx context.Context) {
	e.writer = NewStoreWriter(e.store, e.em)
	go e.writer.Run(ctx)

	// Start workers for every account currently in DB.
	accs, _ := e.store.ListAccounts(ctx)
	for _, a := range accs {
		e.StartAccount(ctx, a.ID)
	}

	<-ctx.Done()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.cancels {
		c()
	}
}

// StartAccount spins up a worker for the given account ID if one isn't already
// running. The worker's context is derived from parent.
func (e *Engine) StartAccount(parent context.Context, id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.workers[id]; ok {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	w := NewAccountWorker(id, e.store, e.secrets, e.writer, e.em)
	e.workers[id] = w
	e.cancels[id] = cancel
	go e.supervise(ctx, id, w)
}

// StopAccount cancels the worker for the given account ID and forgets it.
// The supervise goroutine may still be tearing down for a brief moment after
// this returns.
func (e *Engine) StopAccount(id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.cancels[id]; ok {
		c()
	}
	delete(e.workers, id)
	delete(e.cancels, id)
}

// WorkerFor returns the live worker for an account, or nil if none exists.
func (e *Engine) WorkerFor(id int64) *AccountWorker {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.workers[id]
}

// supervise runs an AccountWorker, recovering from panics and restarting it
// with exponential backoff until ctx is cancelled.
func (e *Engine) supervise(ctx context.Context, id int64, w *AccountWorker) {
	delays := []time.Duration{
		1 * time.Second, 2 * time.Second, 5 * time.Second,
		15 * time.Second, 60 * time.Second, 300 * time.Second,
	}
	attempt := 0
	for {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					slog.Error("account worker panicked", "id", id, "panic", r)
				}
			}()
			w.Run(runCtx)
		}()
		<-done
		cancel()
		select {
		case <-ctx.Done():
			return
		default:
		}
		d := delays[min(attempt, len(delays)-1)]
		attempt++
		slog.Info("restart account worker", "id", id, "in", d)
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
}
