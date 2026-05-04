// Package imap is the only package in spk-mail that knows the IMAP wire
// protocol. It returns plain Go structs to higher layers.
package imap

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
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
	// TCP keepalive is mandatory for IDLE. An IMAP IDLE connection
	// deliberately goes silent — that's the whole point — so any NAT
	// / firewall / SOHO router on the path silently garbage-collects
	// the flow after 5-10 minutes. Without keepalive, our side has no
	// signal the route died and stays parked on a dead socket; the
	// server pushes EXISTS into the void and we never wake up.
	//
	// net.Dialer.KeepAlive only sets SO_KEEPALIVE + TCP_KEEPIDLE.
	// The probe INTERVAL and COUNT are still kernel defaults
	// (tcp_keepalive_intvl=75s × tcp_keepalive_probes=9 ≈ 11 min on
	// stock Linux) — useless for "detect dead IDLE socket within 60
	// seconds" which is what we actually need. Tune all three
	// explicitly via the raw socket controls below.
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: tuneTCPKeepAlive,
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
	// One-line breadcrumb of advertised capabilities right after LOGIN.
	// Useful for diagnosing CONDSTORE / IDLE behaviour against a
	// specific server (the user reports server_modseq=0 even though
	// Yandex documents CONDSTORE; this confirms what's actually
	// advertised). Best-effort: if Capability errors we just don't log.
	if caps, err := wrap.Capabilities(); err == nil {
		keys := make([]string, 0, len(caps))
		for k := range caps {
			keys = append(keys, k)
		}
		slog.Info("imap capabilities", "host", opts.Host, "caps", keys)
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
