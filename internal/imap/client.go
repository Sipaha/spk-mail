// Package imap is the only package in spk-mail that knows the IMAP wire
// protocol. It returns plain Go structs to higher layers.
package imap

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
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
	c *imapclient.Client

	// idleMu guards idleNotifs. The unilateral-data handler closures
	// installed on imapclient.Options run on arbitrary goroutines, so any
	// access to idleNotifs is mediated by this mutex. The mutex is never
	// held while sending on a channel.
	idleMu     sync.Mutex
	idleNotifs chan IdleNotification
}

// pushIdleNotif forwards a unilateral-data notification to the IDLE consumer.
// Non-blocking: drops on full channel, no-op when no consumer is registered.
// The mutex is released BEFORE the channel send so we never hold idleMu while
// a goroutine is sleeping on a full buffer.
func (c *Client) pushIdleNotif(n IdleNotification) {
	c.idleMu.Lock()
	ch := c.idleNotifs
	c.idleMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- n:
	default:
		// drop on full
	}
}

// FolderInfo is the abstract description of an IMAP mailbox returned to
// higher layers (engine, frontend, etc.).
type FolderInfo struct {
	Name      string
	Delimiter string
	Role      string // inbox|sent|drafts|trash|spam|archive|""
}

// Dial opens a TCP (or TLS) connection to the IMAP server, performs LOGIN,
// and returns a ready-to-use Client. Callers must call Close when done.
func Dial(ctx context.Context, opts DialOpts) (*Client, error) {
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	// TCP keepalive is mandatory for IDLE: a freshly-established IMAP
	// connection that goes silent (which IDLE deliberately does — that's
	// the whole point) gets garbage-collected by NAT / firewall / SOHO
	// router idle timers after 5-10 minutes. Without keepalive, our side
	// has no signal that the route died and stays parked on a dead
	// socket; the server pushes EXISTS into the void and we never wake
	// up. SO_KEEPALIVE-derived probes refresh the NAT mapping AND surface
	// dead connections promptly so the runIDLESession bounce can dial
	// fresh instead of waiting on a corpse.
	//
	// 30s is well under typical NAT idle thresholds (Linux conntrack:
	// ~120s for ESTABLISHED-with-no-traffic on most distros; consumer
	// routers commonly 5min) and follows the RFC 1122 spirit of "send
	// keepalives before the silence is suspicious to anyone in the
	// path."
	d := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	// Bootstrap the wrapper first so the unilateral-data closures below can
	// capture *Client by reference. The actual *imapclient.Client is wired
	// in immediately after.
	wrap := &Client{}
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
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		wrap.c = imapclient.New(tlsConn, imapOpts)
	} else {
		wrap.c = imapclient.New(conn, imapOpts)
	}

	if err := wrap.c.Login(opts.Username, opts.Password).Wait(); err != nil {
		_ = wrap.c.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return wrap, nil
}

// Close terminates the IMAP session and releases the underlying connection.
func (c *Client) Close() error { return c.c.Close() }

// ListFolders returns every mailbox the account exposes, with a normalised
// role string when one can be inferred from the LIST attributes.
func (c *Client) ListFolders(_ context.Context) ([]FolderInfo, error) {
	cmd := c.c.List("", "*", nil)
	var out []FolderInfo
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
		out = append(out, FolderInfo{
			Name:      mb.Mailbox,
			Delimiter: delim,
			Role:      mailboxRole(mb),
		})
	}
	if err := cmd.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

func mailboxRole(mb *imap.ListData) string {
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
	if strings.EqualFold(mb.Mailbox, "INBOX") {
		return "inbox"
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

// SplitHostPort is the exported flavour, intended for callers that already
// have a "host:port" string (e.g. config loaders).
func SplitHostPort(addr string) (string, int) { return splitHostPort(addr) }

// Capabilities returns the set of advertised CAPABILITY tokens. Used by the
// engine to decide IDLE vs poll.
func (c *Client) Capabilities() (map[string]bool, error) {
	caps, err := c.c.Capability().Wait()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for k := range caps {
		out[strings.ToUpper(string(k))] = true
	}
	return out, nil
}
