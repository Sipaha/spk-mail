package sync

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/spk/spk-mail/internal/storage"
)

// RawSweeper periodically clears raw_blob_id captures older than the
// retention window and decrements the blob refcounts so the existing
// SweepBlobs reclaims disk on the next pass.
type RawSweeper struct {
	store     storage.Writer
	retention time.Duration
	interval  time.Duration
}

// NewRawSweeper wires a sweeper. interval is fixed at 6h.
func NewRawSweeper(s storage.Writer, retention time.Duration) *RawSweeper {
	return &RawSweeper{store: s, retention: retention, interval: 6 * time.Hour}
}

// Run sweeps once on entry and then every r.interval until ctx
// cancels. Errors are logged; the loop never returns early.
func (r *RawSweeper) Run(ctx context.Context) {
	r.sweepOnceAt(ctx, time.Now())
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.sweepOnceAt(ctx, time.Now())
		}
	}
}

// sweepOnceAt is the unit-testable inner sweep. now is the wall clock
// to evaluate retention against; production passes time.Now().
// Returns the number of cleared captures.
func (r *RawSweeper) sweepOnceAt(ctx context.Context, now time.Time) int {
	cutoff := now.Add(-r.retention).Unix()
	cleared, err := r.store.SweepExpiredRaw(ctx, cutoff)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Warn("raw sweep: SweepExpiredRaw failed", "err", err)
		}
		return 0
	}
	failures := 0
	for _, blobID := range cleared {
		if _, err := r.store.DecBlobRef(ctx, blobID); err != nil {
			slog.Warn("raw sweep: DecBlobRef failed", "blob_id", blobID, "err", err)
			failures++
		}
	}
	if len(cleared) > 0 {
		slog.Info("raw sweep complete", "cleared", len(cleared), "errors", failures)
	}
	return len(cleared)
}

// Compile-time storage.Writer signature check is implicit via SweepExpiredRaw
// + DecBlobRef calls — if either is missing from the interface, compilation
// fails here.
