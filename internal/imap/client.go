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
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	var c *imapclient.Client
	if opts.UseTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: opts.Host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		c = imapclient.New(tlsConn, &imapclient.Options{})
	} else {
		c = imapclient.New(conn, &imapclient.Options{})
	}

	if err := c.Login(opts.Username, opts.Password).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return &Client{c: c}, nil
}

// Close terminates the IMAP session and releases the underlying connection.
func (c *Client) Close() error { return c.c.Close() }

// ListFolders returns every mailbox the account exposes, with a normalised
// role string when one can be inferred from the LIST attributes.
func (c *Client) ListFolders(ctx context.Context) ([]FolderInfo, error) {
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
func (c *Client) Capabilities(ctx context.Context) (map[string]bool, error) {
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
