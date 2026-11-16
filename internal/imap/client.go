// Package imap is the only package in spk-mail that knows the IMAP wire
// protocol. It returns plain Go structs to higher layers.
package imap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	defaultDialTimeout    = 20 * time.Second
	defaultCommandTimeout = 30 * time.Second
	defaultStreamTimeout  = 60 * time.Second
)

// DialOpts is the input to Dial.
type DialOpts struct {
	Host     string
	Port     int
	Username string
	Password string
	UseTLS   bool
}

// Client is a thin wrapper around imapclient.Client. The rest of spk-mail
// only sees the methods on this type and the plain Go structs it returns.
type Client struct {
	c       *imapclient.Client
	rawConn net.Conn

	closed     atomic.Bool
	cmdTimeout time.Duration

	capsMu sync.Mutex
	caps   map[string]bool

	// idleMu guards idleNotifs and idleMissed. The unilateral-data handler
	// closures installed on imapclient.Options run on arbitrary goroutines, so
	// any access to them is mediated by this mutex. The mutex is never held
	// while sending on a channel.
	idleMu     sync.Mutex
	idleNotifs chan IdleNotification
	// idleMissed records that the server pushed a notification we could not
	// hand to anyone — either no IDLE consumer was registered (the window
	// between DONE and the next IDLE, during which the caller runs a FETCH on
	// this same connection per RFC 2177) or the consumer's buffer was full.
	// The server will not repeat an EXISTS, so without this flag that arrival
	// would stay invisible until the next one. Idle() converts a set flag into
	// a synthetic EXISTS on the fresh channel, which makes the caller re-sync.
	idleMissed bool
}

// SetCommandTimeout overrides the per-command timeout used for non-IDLE IMAP
// operations and DONE waits. Intended for tests that inject half-open stalls.
func (c *Client) SetCommandTimeout(d time.Duration) {
	c.cmdTimeout = d
}

func (c *Client) commandTimeout() time.Duration {
	if c.cmdTimeout > 0 {
		return c.cmdTimeout
	}
	return defaultCommandTimeout
}

func (c *Client) streamTimeout() time.Duration {
	if c.cmdTimeout > 0 {
		return c.cmdTimeout
	}
	return defaultStreamTimeout
}

// pushIdleNotif forwards a unilateral-data notification to the IDLE consumer.
// Non-blocking; when the notification cannot be delivered it is recorded in
// idleMissed rather than dropped, so the next Idle() replays it. The mutex is
// released BEFORE the channel send so we never hold idleMu while a goroutine is
// sleeping on a full buffer.
func (c *Client) pushIdleNotif(n IdleNotification) {
	c.idleMu.Lock()
	ch := c.idleNotifs
	if ch == nil {
		c.idleMissed = true
		c.idleMu.Unlock()
		return
	}
	c.idleMu.Unlock()
	select {
	case ch <- n:
	default:
		c.idleMu.Lock()
		c.idleMissed = true
		c.idleMu.Unlock()
	}
}

// FolderInfo is the abstract description of an IMAP mailbox returned to
// higher layers (engine, frontend, etc.).
type FolderInfo struct {
	Name      string
	Delimiter string
	Role      string // inbox|sent|drafts|trash|spam|archive|""
	NoSelect  bool
}

// Dial opens a TCP (or TLS) connection to the IMAP server, performs LOGIN,
// and returns a ready-to-use Client. Callers must call Close when done.
func Dial(ctx context.Context, opts DialOpts) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialCtx := ctx
	var dialCancel context.CancelFunc
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > defaultDialTimeout {
		dialCtx, dialCancel = context.WithTimeout(ctx, defaultDialTimeout)
		defer dialCancel()
	}

	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	// TCP keepalive + TCP_USER_TIMEOUT detect dead IDLE or half-open sockets
	// instead of leaving reads/writes parked indefinitely.
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: tuneTCPKeepAlive,
	}
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	// Bootstrap the wrapper first so the unilateral-data closures below can
	// capture *Client by reference, and so dialCtx cancellation closes rawConn.
	wrap := &Client{
		rawConn:    conn,
		cmdTimeout: defaultCommandTimeout,
	}
	stopDialClose := context.AfterFunc(dialCtx, func() {
		_ = wrap.Close()
	})
	defer stopDialClose()

	imapOpts := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				// EXISTS arrives as a non-nil NumMessages. Flag-only
				// updates (Flags / PermanentFlags) are ignored — Idle()
				// callers only care about messages appearing.
				if data == nil || data.NumMessages == nil {
					return
				}
				wrap.pushIdleNotif(IdleNotification{Kind: NotifExists})
			},
			Expunge: func(_ uint32) {
				// EXPUNGE notifications are not surfaced — see NotifKind
				// docstring in idle.go.
			},
			Fetch: func(msg *imapclient.FetchMessageData) {
				// FETCH notifications are not surfaced (see idle.go).
				// We still drain the message body — the client read
				// loop will wedge if we don't consume FETCH items
				// before returning.
				if msg != nil {
					_, _ = msg.Collect()
				}
			},
		},
	}

	if opts.UseTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: opts.Host})
		if err := tlsConn.HandshakeContext(dialCtx); err != nil {
			_ = wrap.Close()
			return nil, err
		}
		wrap.c = imapclient.New(tlsConn, imapOpts)
	} else {
		wrap.c = imapclient.New(conn, imapOpts)
	}

	if err := wrap.c.Login(opts.Username, opts.Password).Wait(); err != nil {
		_ = wrap.Close()
		if dialErr := dialCtx.Err(); dialErr != nil {
			return nil, fmt.Errorf("imap login: %w", dialErr)
		}
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return wrap, nil
}

// Close terminates the IMAP session and releases the underlying connection.
// Safe to call multiple times or concurrently with blocked I/O.
func (c *Client) Close() error {
	alreadyClosed := c.closed.Swap(true)
	var rawErr error
	if c.rawConn != nil {
		// Force blocked TLS/TCP reads and writes to wake up immediately.
		_ = c.rawConn.SetDeadline(time.Now())
		rawErr = c.rawConn.Close()
	}
	var clientErr error
	if c.c != nil {
		clientErr = c.c.Close()
	}
	if alreadyClosed {
		return net.ErrClosed
	}
	if clientErr != nil && !errors.Is(clientErr, net.ErrClosed) && !errors.Is(clientErr, io.ErrClosedPipe) {
		return clientErr
	}
	if rawErr != nil && !errors.Is(rawErr, net.ErrClosed) && !errors.Is(rawErr, io.ErrClosedPipe) {
		return rawErr
	}
	return nil
}

// IsClosed reports whether Close was called or the underlying IMAP read loop
// has already terminated (e.g. server BYE / TCP EOF / reset).
func (c *Client) IsClosed() bool {
	if c == nil || c.closed.Load() || c.c == nil {
		return true
	}
	select {
	case <-c.c.Closed():
		return true
	default:
		return false
	}
}

// runWithTimeout runs fn bounded by ctx and timeout d. On timeout or ctx
// cancellation it closes the underlying connection so blocked IMAP commands
// fail promptly instead of leaking goroutines or hanging half-open sockets.
func (c *Client) runWithTimeout(ctx context.Context, d time.Duration, op string, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	opCtx := ctx
	var cancel context.CancelFunc
	if d > 0 {
		opCtx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	stop := context.AfterFunc(opCtx, func() {
		_ = c.Close()
	})
	defer stop()

	err := fn()
	if err != nil {
		if opErr := opCtx.Err(); opErr != nil {
			return fmt.Errorf("imap %s: %w", op, opErr)
		}
		return err
	}
	return nil
}

// ListFolders returns every mailbox the account exposes, with a normalised
// role string when one can be inferred from the LIST attributes.
func (c *Client) ListFolders(ctx context.Context) ([]FolderInfo, error) {
	var out []FolderInfo
	err := c.runWithTimeout(ctx, c.commandTimeout(), "list", func() error {
		cmd := c.c.List("", "*", nil)
		defer cmd.Close()
		var list []FolderInfo
		for {
			mb := cmd.Next()
			if mb == nil {
				break
			}
			// RFC 3501 allows a NIL hierarchy delimiter (flat namespace), which
			// the parser surfaces as rune 0. string(rune(0)) is "\x00", not "",
			// so guard the conversion to keep flat-namespace folders correct.
			delim := ""
			if mb.Delim != 0 {
				delim = string(mb.Delim)
			}
			list = append(list, FolderInfo{
				Name:      mb.Mailbox,
				Delimiter: delim,
				Role:      mailboxRole(mb),
				NoSelect:  mailboxNoSelect(mb),
			})
		}
		if err := cmd.Close(); err != nil {
			return err
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func mailboxNoSelect(mb *imap.ListData) bool {
	for _, a := range mb.Attrs {
		if a == imap.MailboxAttrNoSelect || a == imap.MailboxAttrNonExistent {
			return true
		}
	}
	return false
}

func mailboxRole(mb *imap.ListData) string {
	// RFC 3501 reserves INBOX case-insensitively as the primary inbox; check
	// name before auxiliary attributes so a tagged INBOX never loses its role.
	if strings.EqualFold(mb.Mailbox, "INBOX") {
		return "inbox"
	}
	for _, a := range mb.Attrs {
		switch a {
		case imap.MailboxAttrSent:
			return "sent"
		case imap.MailboxAttrDrafts:
			return "drafts"
		case imap.MailboxAttrTrash:
			return "trash"
		case imap.MailboxAttrJunk:
			return "spam"
		case imap.MailboxAttrArchive, imap.MailboxAttrAll:
			return "archive"
		}
	}
	return ""
}

// splitHostPort splits "host:port" into its parts. Used by tests; kept
// unexported because nothing outside this package needs it yet.
func splitHostPort(addr string) (string, int) {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return host, p
}

// Capabilities returns the set of advertised CAPABILITY tokens. Used by the
// engine to decide IDLE vs poll.
func (c *Client) Capabilities() (map[string]bool, error) {
	return c.CapabilitiesWithContext(context.Background())
}

// CapabilitiesWithContext returns cached post-login capabilities or fetches
// CAPABILITY bounded by ctx and the client's command timeout.
func (c *Client) CapabilitiesWithContext(ctx context.Context) (map[string]bool, error) {
	c.capsMu.Lock()
	if c.caps != nil {
		out := maps.Clone(c.caps)
		c.capsMu.Unlock()
		return out, nil
	}
	c.capsMu.Unlock()

	var capSet map[string]bool
	err := c.runWithTimeout(ctx, c.commandTimeout(), "capability", func() error {
		caps := c.c.Caps()
		if caps == nil {
			var err error
			caps, err = c.c.Capability().Wait()
			if err != nil {
				return err
			}
		}
		out := make(map[string]bool, len(caps))
		for k := range caps {
			out[strings.ToUpper(string(k))] = true
		}
		capSet = out
		return nil
	})
	if err != nil {
		return nil, err
	}

	c.capsMu.Lock()
	c.caps = capSet
	out := maps.Clone(c.caps)
	c.capsMu.Unlock()
	return out, nil
}

// IsCommandRejection distinguishes a tagged command response from a broken
// transport. A denied mailbox must not block independent mailbox operations.
func IsCommandRejection(err error) bool {
	var rejected *imap.Error
	return errors.As(err, &rejected) && (rejected.Type == imap.StatusResponseTypeNo || rejected.Type == imap.StatusResponseTypeBad)
}
