package sync

import (
	"context"
	"errors"
	"log/slog"
	"os"
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
	store   storage.Writer
	secrets *secrets.Store
	em      *api.Emitter

	mu              sync.Mutex
	workers         map[int64]*AccountWorker
	cancels         map[int64]context.CancelFunc
	writer          *StoreWriter
	downloaders     map[int64]*AttachmentDownloader
	downloaderStops map[int64]context.CancelFunc
	attachDir       string

	// rootCtx is the engine's process-wide context, captured by Run. Workers
	// derive from it so they outlive the per-request HTTP ctx that called
	// StartAccount via AddAccount.
	rootCtx context.Context

	// wg covers every goroutine the engine spawned (writer, supervisors,
	// downloaders). Run blocks on wg.Wait before returning so callers know
	// the engine has fully drained before, e.g., closing the SQLite handle.
	wg sync.WaitGroup
}

// NewEngine constructs an Engine. It performs no I/O.
func NewEngine(s storage.Writer, sec *secrets.Store, em *api.Emitter) *Engine {
	return &Engine{
		store:           s,
		secrets:         sec,
		em:              em,
		workers:         map[int64]*AccountWorker{},
		cancels:         map[int64]context.CancelFunc{},
		downloaders:     map[int64]*AttachmentDownloader{},
		downloaderStops: map[int64]context.CancelFunc{},
	}
}

// NewEngineWithDir constructs an Engine that will additionally start an
// AttachmentDownloader per account, writing blobs under attachDir. If
// attachDir is empty no downloaders are spawned (matching NewEngine).
func NewEngineWithDir(s storage.Writer, sec *secrets.Store, em *api.Emitter, attachDir string) *Engine {
	e := NewEngine(s, sec, em)
	e.attachDir = attachDir
	return e
}

// Run starts the StoreWriter and a worker per account currently in the DB.
// It blocks until ctx is cancelled, then signals all workers to stop and
// waits for them (and the writer / downloaders) to exit before returning.
func (e *Engine) Run(ctx context.Context) {
	// Initialise rootCtx and writer in a single locked critical section so a
	// concurrent StartAccount call (e.g. user-driven AddAccount racing engine
	// startup) can never observe e.writer == nil under the same lock.
	e.mu.Lock()
	e.rootCtx = ctx
	e.writer = NewStoreWriter(e.store, e.em)
	e.mu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.writer.Run(ctx)
	}()

	// Start workers for every account currently in DB.
	accs, _ := e.store.ListAccounts(ctx)
	for _, a := range accs {
		e.StartAccount(ctx, a.ID)
	}

	// Best-effort startup blob maintenance, gated on attachDir (the
	// data-dir root BlobPath composes under) AND on the
	// SPK_DISABLE_MAINTENANCE env var being unset. The killswitch
	// exists because the backfill pass — re-hashing legacy
	// per-message attachment files into the content-addressed store
	// — is heavy I/O that competes with the sync writer; on a huge
	// inbox it can starve incoming-message inserts on first run
	// after upgrade. Setting SPK_DISABLE_MAINTENANCE=1 is the escape
	// hatch if the user observes that.
	//
	// Two passes, in order:
	//   1. Backfill legacy files into the content-addressed store
	//      (idempotent, capped at backfillBatchSize per pass — the
	//      remaining rows roll over to the next start).
	//   2. GC sweep for refcount=0 blobs (reclaim disk after account
	//      removal / UIDVALIDITY purge / a prior crash between the
	//      message-DELETE tx and a previous sweep).
	//
	// Backfill must come first so newly-created blob rows don't
	// briefly look like sweep candidates.
	//
	// We also gate the start of the maintenance goroutine on a
	// 30-second delay after Run, so the first real-time sync the
	// user sees on startup gets uncontended writer access. (The
	// initial bulk sync is started from StartAccount above and runs
	// concurrently — the delay is for the IDLE / poll loops that
	// kick in once initial sync per folder completes.)
	if e.attachDir != "" && os.Getenv("SPK_DISABLE_MAINTENANCE") != "1" {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			migrated, bErr := e.store.BackfillLegacyAttachments(ctx, e.attachDir)
			if bErr != nil && !errors.Is(bErr, context.Canceled) {
				slog.Warn("legacy attachments backfill failed", "err", bErr)
			} else if migrated > 0 {
				slog.Info("legacy attachments backfill pass complete", "migrated", migrated)
			}
			rows, bytes, err := e.store.SweepBlobs(ctx, e.attachDir)
			if err != nil {
				slog.Warn("startup blob sweep failed", "err", err)
				return
			}
			if rows > 0 {
				slog.Info("startup blob sweep complete",
					"rows_deleted", rows, "bytes_reclaimed", bytes)
			}
		}()
	}

	<-ctx.Done()
	e.mu.Lock()
	for _, c := range e.cancels {
		c()
	}
	for _, c := range e.downloaderStops {
		c()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

// StartAccount spins up a worker for the given account ID if one isn't already
// running. The worker's context is derived from the engine's process-wide root
// context (captured by Run) so it survives the per-request HTTP context that
// may have called StartAccount via AddAccount. The parent argument is kept for
// API compatibility but is only used as a fallback when Run hasn't been called
// yet (tests).
func (e *Engine) StartAccount(parent context.Context, id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.workers[id]; ok {
		return
	}
	base := e.rootCtx
	if base == nil {
		base = parent
	}
	ctx, cancel := context.WithCancel(base)
	w := NewAccountWorker(id, e.store, e.secrets, e.writer, e.em)
	e.workers[id] = w
	e.cancels[id] = cancel
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.supervise(ctx, id, w)
	}()

	e.registerDownloaderForAccountLocked(base, id)
}

// registerDownloaderForAccountLocked spawns an AttachmentDownloader for id
// under base ctx. Idempotent (no-op when one is already registered), and a
// no-op when attachDir is empty (NewEngine without an attachment dir).
//
// Tests that just want to assert the wiring (account → downloader entry)
// can call this directly under e.mu.Lock and avoid spawning supervise +
// its tier-table dial-retry loop, which busy-loops imap.Dial against the
// fake account's port=1 until ctx cancel.
//
// Caller must hold e.mu.
func (e *Engine) registerDownloaderForAccountLocked(base context.Context, id int64) {
	if e.attachDir == "" {
		return
	}
	if _, ok := e.downloaders[id]; ok {
		return
	}
	dCtx, dCancel := context.WithCancel(base)
	d := NewAttachmentDownloader(id, e.store, e.secrets, e.em, e.attachDir)
	e.downloaders[id] = d
	e.downloaderStops[id] = dCancel
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		d.Run(dCtx)
	}()
}

// StopAccount cancels the worker and the AttachmentDownloader for the given
// account ID and forgets them. The supervise / downloader goroutines may
// still be tearing down for a brief moment after this returns.
func (e *Engine) StopAccount(id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.cancels[id]; ok {
		c()
	}
	if c, ok := e.downloaderStops[id]; ok {
		c()
	}
	delete(e.workers, id)
	delete(e.cancels, id)
	delete(e.downloaders, id)
	delete(e.downloaderStops, id)
}

// WorkerFor returns the live worker for an account, or nil if none exists.
func (e *Engine) WorkerFor(id int64) *AccountWorker {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.workers[id]
}

// supervise runs an AccountWorker, recovering from panics and restarting it
// with exponential backoff until ctx is cancelled. The attempt counter is
// reset to 0 once a worker has been healthy for ≥ the longest delay
// (5 min); a transient daytime outage that bounces the worker 6+ times
// then heals would otherwise leave the next failure pinned at the 300s tier
// for the rest of the process lifetime.
func (e *Engine) supervise(ctx context.Context, id int64, w *AccountWorker) {
	delays := []time.Duration{
		1 * time.Second, 2 * time.Second, 5 * time.Second,
		15 * time.Second, 60 * time.Second, 300 * time.Second,
	}
	const healthyThreshold = 5 * time.Minute
	attempt := 0
	for {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		startedAt := time.Now()
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
		if time.Since(startedAt) >= healthyThreshold {
			attempt = 0
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Belt-and-braces against the StopAccount → re-add race. ctx
		// cancellation is the primary stop signal, but if StopAccount has
		// already removed our entry (mutex held during the map mutation)
		// we must not start another iteration and resurrect a worker for
		// an account the engine no longer owns.
		if e.WorkerFor(id) != w {
			return
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
