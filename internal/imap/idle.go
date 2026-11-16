package imap

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// NotifKind identifies the type of unilateral update observed on the
// currently-selected mailbox during an IDLE session.
type NotifKind string

const (
	// NotifExists means the server reported an EXISTS update — typically a
	// new message has arrived in the selected mailbox. EXPUNGE / FETCH
	// updates are observed by the underlying handler but not surfaced here:
	// the only consumer (account_worker.runIDLE) re-syncs via syncFolder
	// after EXISTS (UID SEARCH + FetchByUIDs), which already picks up flag
	// changes and doesn't need a separate FETCH notification.
	NotifExists NotifKind = "exists"
)

// IdleNotification is a single unilateral update delivered to a caller of
// (*Client).Idle.
type IdleNotification struct {
	Kind NotifKind
	// UID is best-effort and may be zero. The current implementation does
	// not extract UIDs from FETCH responses (doing so would require
	// blocking the client read loop), so this field is reserved for a
	// future enhancement.
	UID int64
}

// Idle starts an IDLE loop on the currently-selected mailbox. Notifications
// observed via the imapclient.UnilateralDataHandler installed at Dial time
// are forwarded to ch. When the underlying connection closes or IDLE fails,
// Idle closes ch so callers break out immediately rather than hanging until a
// session timer fires.
//
// See IdleWithErr for callers that need the initial IDLE error or the DONE
// completion status.
func (c *Client) Idle(ctx context.Context, ch chan<- IdleNotification) func() {
	stop, err := c.IdleWithErr(ctx, ch)
	if err != nil {
		return func() {}
	}
	return func() { _ = stop() }
}

// IdleWithErr enters IMAP IDLE on the currently-selected mailbox and waits
// until the server acknowledges `+ idling` before returning. If entering IDLE
// fails (e.g. tagged NO/BAD rejection or dead socket), ch is closed and the
// error is returned immediately instead of spinning silently.
//
// The returned stop function sends DONE and waits for the server's tagged
// completion (bounded by command timeout) while clearing the notification slot
// before DONE so any push arriving during the DONE→FETCH→IDLE gap sets
// idleMissed for replay on the next IdleWithErr call. If the underlying
// connection dies while idling, ch is closed immediately and stop returns the
// connection failure.
func (c *Client) IdleWithErr(ctx context.Context, ch chan<- IdleNotification) (func() error, error) {
	c.idleMu.Lock()
	internalCh := make(chan IdleNotification, 16)
	c.idleNotifs = internalCh
	missed := c.idleMissed
	c.idleMissed = false
	c.idleMu.Unlock()

	// Replay a notification the server pushed while nobody was listening —
	// the DONE→FETCH→IDLE window in the caller, or a full buffer. Registering
	// the channel and taking the flag happen under the same lock, so a
	// notification arriving during the swap either lands in internalCh or sets
	// the flag again for the next Idle; it cannot fall between the two.
	if missed {
		internalCh <- IdleNotification{Kind: NotifExists}
	}

	var idle *imapclient.IdleCommand
	if err := c.runWithTimeout(ctx, c.commandTimeout(), "idle start", func() error {
		var e error
		idle, e = c.c.Idle()
		return e
	}); err != nil {
		c.idleMu.Lock()
		if c.idleNotifs == internalCh {
			c.idleNotifs = nil
		}
		if missed || len(internalCh) > 0 {
			c.idleMissed = true
		}
		c.idleMu.Unlock()
		close(ch)
		return nil, err
	}

	stopCh := make(chan struct{})
	var stopOnce sync.Once
	exited := make(chan struct{})
	var stopErr error

	terminateIdle := func(cur *imapclient.IdleCommand) error {
		c.idleMu.Lock()
		if c.idleNotifs == internalCh {
			c.idleNotifs = nil
		}
		c.idleMu.Unlock()

		if cur == nil {
			return nil
		}
		done := make(chan error, 1)
		go func() {
			closeErr := cur.Close()
			waitErr := cur.Wait()
			if closeErr != nil {
				done <- closeErr
			} else {
				done <- waitErr
			}
		}()
		waitTimeout := c.commandTimeout()
		if waitTimeout > 10*time.Second {
			waitTimeout = 10 * time.Second
		}
		timer := time.NewTimer(waitTimeout)
		defer timer.Stop()
		select {
		case err := <-done:
			return err
		case <-timer.C:
			_ = c.Close()
			if err := <-done; err != nil {
				return err
			}
			return fmt.Errorf("imap idle DONE: %w", context.DeadlineExceeded)
		}
	}

	idleClosedErr := func(cur *imapclient.IdleCommand) error {
		c.idleMu.Lock()
		if c.idleNotifs == internalCh {
			c.idleNotifs = nil
		}
		c.idleMu.Unlock()
		if cur != nil {
			if err := cur.Wait(); err != nil {
				return fmt.Errorf("imap idle connection closed: %w", err)
			}
		}
		return fmt.Errorf("imap idle connection closed: %w", io.ErrUnexpectedEOF)
	}

	go func() {
		defer close(exited)
		defer close(ch)

		refresh := time.NewTimer(27 * time.Minute)
		defer refresh.Stop()

		for {
			select {
			case <-c.c.Closed():
				stopErr = idleClosedErr(idle)
				_ = c.Close()
				return
			case <-stopCh:
				stopErr = terminateIdle(idle)
				return
			case <-ctx.Done():
				_ = terminateIdle(idle)
				stopErr = ctx.Err()
				return
			case <-refresh.C:
				if err := terminateIdle(idle); err != nil {
					stopErr = err
					_ = c.Close()
					return
				}
				c.idleMu.Lock()
				c.idleNotifs = internalCh
				c.idleMu.Unlock()
				var nextIdle *imapclient.IdleCommand
				if err := c.runWithTimeout(ctx, c.commandTimeout(), "idle refresh", func() error {
					var e error
					nextIdle, e = c.c.Idle()
					return e
				}); err != nil {
					c.idleMu.Lock()
					if c.idleNotifs == internalCh {
						c.idleNotifs = nil
					}
					c.idleMu.Unlock()
					stopErr = err
					return
				}
				idle = nextIdle
				refresh.Reset(27 * time.Minute)
			case n := <-internalCh:
				select {
				case ch <- n:
				case <-c.c.Closed():
					stopErr = idleClosedErr(idle)
					_ = c.Close()
					return
				case <-stopCh:
					c.idleMu.Lock()
					c.idleMissed = true
					c.idleMu.Unlock()
					stopErr = terminateIdle(idle)
					return
				case <-ctx.Done():
					c.idleMu.Lock()
					c.idleMissed = true
					c.idleMu.Unlock()
					_ = terminateIdle(idle)
					stopErr = ctx.Err()
					return
				}
			}
		}
	}()

	return func() error {
		stopOnce.Do(func() { close(stopCh) })
		<-exited
		return stopErr
	}, nil
}

// HasIDLE reports whether the server advertises the IDLE capability (or IMAP4rev2).
func (c *Client) HasIDLE() bool {
	return c.HasIDLEContext(context.Background())
}

// HasIDLEContext reports whether the server supports IDLE, bounded by ctx.
func (c *Client) HasIDLEContext(ctx context.Context) bool {
	caps, err := c.CapabilitiesWithContext(ctx)
	if err != nil {
		return false
	}
	return caps["IDLE"] || caps["IMAP4REV2"]
}

// HasCondStore reports whether the server advertises CONDSTORE
// (RFC 7162). The Select wrapper consults this before passing
// CondStore=true in the SELECT options — sending the option to a
// server that doesn't advertise it is a protocol error (CLIENTBUG /
// BAD), not just a silent no-op.
func (c *Client) HasCondStore() bool {
	return c.HasCondStoreContext(context.Background())
}

// HasCondStoreContext reports whether the server advertises CONDSTORE, bounded by ctx.
func (c *Client) HasCondStoreContext(ctx context.Context) bool {
	caps, err := c.CapabilitiesWithContext(ctx)
	if err != nil {
		return false
	}
	return caps["CONDSTORE"]
}
