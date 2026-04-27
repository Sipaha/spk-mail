package imap

import (
	"context"
	"sync"
	"time"
)

// NotifKind identifies the type of unilateral update observed on the
// currently-selected mailbox during an IDLE session.
type NotifKind string

const (
	// NotifExists means the server reported an EXISTS update — typically a
	// new message has arrived in the selected mailbox.
	NotifExists NotifKind = "exists"
	// NotifExpunge means the server reported an EXPUNGE — a message was
	// removed from the selected mailbox.
	NotifExpunge NotifKind = "expunge"
	// NotifFetch means the server pushed a FETCH update, generally a flag
	// change on an existing message.
	NotifFetch NotifKind = "fetch"
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
// are forwarded to ch (drop-on-full at the Client side, blocking on ch).
//
// Idle blocks until the first IDLE command is acknowledged by the server,
// so callers that schedule mailbox writes immediately afterwards can rely
// on those writes producing notifications. (If the initial IDLE attempt
// fails the call still returns; the loop will reconnect/retry every 2s.)
//
// The returned stop function is idempotent: it ends the IDLE loop and
// closes ch. The IDLE command is automatically restarted every 28 minutes
// to dodge server inactivity timeouts.
func (c *Client) Idle(ctx context.Context, ch chan<- IdleNotification) func() {
	c.idleMu.Lock()
	c.idleNotifs = make(chan IdleNotification, 16)
	internalCh := c.idleNotifs
	c.idleMu.Unlock()

	stopCh := make(chan struct{})
	var stopOnce sync.Once
	ready := make(chan struct{})
	exited := make(chan struct{})

	go func() {
		defer close(exited)
		defer func() {
			c.idleMu.Lock()
			c.idleNotifs = nil
			c.idleMu.Unlock()
			close(ch)
		}()

		first := true
		for {
			select {
			case <-stopCh:
				if first {
					close(ready)
				}
				return
			case <-ctx.Done():
				if first {
					close(ready)
				}
				return
			default:
			}

			idle, err := c.c.Idle()
			if err != nil {
				if first {
					// Unblock the caller even on a failed first attempt
					// so Idle() does not deadlock on a broken server.
					close(ready)
					first = false
				}
				// Brief backoff before the next attempt; honour stop /
				// ctx cancellation so we never leak the goroutine.
				select {
				case <-stopCh:
					return
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}

			if first {
				close(ready)
				first = false
			}

			refresh := time.NewTimer(28 * time.Minute)
			done := false
			for !done {
				select {
				case <-stopCh:
					_ = idle.Close()
					refresh.Stop()
					return
				case <-ctx.Done():
					_ = idle.Close()
					refresh.Stop()
					return
				case <-refresh.C:
					_ = idle.Close()
					done = true
				case n := <-internalCh:
					select {
					case ch <- n:
					case <-stopCh:
						_ = idle.Close()
						refresh.Stop()
						return
					case <-ctx.Done():
						_ = idle.Close()
						refresh.Stop()
						return
					}
				}
			}
		}
	}()

	<-ready
	return func() {
		stopOnce.Do(func() { close(stopCh) })
		<-exited
	}
}

// HasIDLE reports whether the server advertises the IDLE capability.
//
// The ctx parameter is accepted for API symmetry with the rest of the
// package; the underlying capability lookup does not currently use it.
func (c *Client) HasIDLE(_ context.Context) bool {
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		return false
	}
	return caps["IDLE"]
}
