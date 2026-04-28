package imap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// FolderState captures the bits of SELECT response the engine cares about.
type FolderState struct {
	UIDValidity int64
	UIDNext     int64
	Exists      int64
}

// Select issues a SELECT for the given mailbox and returns its state.
func (c *Client) Select(_ context.Context, mailbox string) (FolderState, error) {
	sel, err := c.c.Select(mailbox, nil).Wait()
	if err != nil {
		return FolderState{}, err
	}
	return FolderState{
		UIDValidity: int64(sel.UIDValidity),
		UIDNext:     int64(sel.UIDNext),
		Exists:      int64(sel.NumMessages),
	}, nil
}

// FetchedMessage is the per-message payload streamed by FetchSinceUID.
type FetchedMessage struct {
	UID      int64
	Flags    []string
	Internal int64 // INTERNALDATE epoch seconds
	Raw      []byte
}

// FetchSinceUID streams messages with UID > sinceUID via UID FETCH using an
// open-ended `n:*` range. Use FetchSinceUIDRange for a bounded range.
// `body` true = include full RFC822 (used during initial sync); false = envelope only (not used yet).
func (c *Client) FetchSinceUID(ctx context.Context, sinceUID int64, body bool) (<-chan FetchedMessage, <-chan error) {
	return c.fetchUIDRange(ctx, imap.UID(sinceUID+1), 0, body)
}

// FetchSinceUIDRange streams messages with UID in (sinceUID, untilUID]. Both
// bounds are inclusive on the upper side per IMAP convention. Used by callers
// that want to bound the response size of a single FETCH (e.g. batched bulk
// sync of a 90k-message mailbox where one open-ended FETCH would otherwise
// time out).
func (c *Client) FetchSinceUIDRange(ctx context.Context, sinceUID, untilUID int64, body bool) (<-chan FetchedMessage, <-chan error) {
	return c.fetchUIDRange(ctx, imap.UID(sinceUID+1), imap.UID(untilUID), body)
}

func (c *Client) fetchUIDRange(ctx context.Context, fromUID, toUID imap.UID, body bool) (<-chan FetchedMessage, <-chan error) {
	out := make(chan FetchedMessage, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		// Build the UID range. AddRange's stop=0 represents '*'.
		var seq imap.UIDSet
		seq.AddRange(fromUID, toUID)

		opts := &imap.FetchOptions{
			Flags:        true,
			InternalDate: true,
			UID:          true,
		}
		if body {
			opts.BodySection = []*imap.FetchItemBodySection{
				{Specifier: imap.PartSpecifierNone, Peek: true},
			}
		}

		// Note: imapclient.Client.Fetch dispatches to UID FETCH automatically
		// when numSet is an imap.UIDSet (see imapclient/fetch.go).
		cmd := c.c.Fetch(seq, opts)
		// Ensure the command is drained on any early return; the upstream
		// imapclient read goroutine forwards FETCH responses synchronously
		// into a bounded channel and would wedge if we abandoned cmd while
		// it still had pending messages. cmd.Close is idempotent.
		defer cmd.Close()

		for {
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}

			msg := cmd.Next()
			if msg == nil {
				break
			}
			data, err := msg.Collect()
			if err != nil {
				errCh <- err
				return
			}
			fm := FetchedMessage{
				UID:      int64(data.UID),
				Internal: data.InternalDate.Unix(),
			}
			for _, f := range data.Flags {
				fm.Flags = append(fm.Flags, string(f))
			}
			if body {
				for _, sec := range data.BodySection {
					if len(sec.Bytes) > 0 {
						fm.Raw = sec.Bytes
						break
					}
				}
			}

			select {
			case out <- fm:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}

		if err := cmd.Close(); err != nil {
			errCh <- err
		}
	}()
	return out, errCh
}

// FetchBodyPart fetches a single MIME part of a message identified by its
// IMAP BODYSTRUCTURE part path (e.g. "1.2") via UID FETCH BODY.PEEK[<part>].
// The returned bytes are decoded according to the part's
// Content-Transfer-Encoding header (base64 / quoted-printable), so the
// caller receives the actual binary/text payload. The MIME headers of the
// part are fetched in the same FETCH (BODY.PEEK[<part>.MIME]) so we don't
// need a second round-trip.
func (c *Client) FetchBodyPart(ctx context.Context, uid int64, partID string) ([]byte, error) {
	seq := imap.UIDSetNum(imap.UID(uid))
	part := parsePartPath(partID)
	opts := &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{
			{Specifier: imap.PartSpecifierMIME, Part: part, Peek: true},
			{Specifier: imap.PartSpecifierNone, Part: part, Peek: true},
		},
	}
	// Fetch dispatches to UID FETCH automatically when numSet is a UIDSet
	// (see imapclient/fetch.go). Mirroring FetchSinceUID's pattern keeps the
	// read-loop drained on early return; FetchCommand.Close is idempotent.
	cmd := c.c.Fetch(seq, opts)
	defer cmd.Close()

	var body, mimeHdr []byte
	var haveBody bool
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		msg := cmd.Next()
		if msg == nil {
			break
		}
		data, err := msg.Collect()
		if err != nil {
			return nil, err
		}
		// Match by Specifier — FetchBodySectionBuffer.Section exposes the
		// FetchItemBodySection that produced this buffer (see
		// imapclient/fetch.go:484).
		for _, sec := range data.BodySection {
			if sec.Section == nil {
				continue
			}
			switch sec.Section.Specifier {
			case imap.PartSpecifierMIME:
				mimeHdr = sec.Bytes
			case imap.PartSpecifierNone:
				body = sec.Bytes
				haveBody = true
			}
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, err
	}
	if !haveBody {
		return nil, fmt.Errorf("imap: no body part %q for uid %d", partID, uid)
	}

	cte := cteFromMIMEHeaders(mimeHdr)
	switch cte {
	case "base64":
		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(body)))
		if err != nil {
			return nil, fmt.Errorf("imap: decode base64: %w", err)
		}
		body = decoded
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
		if err != nil {
			return nil, fmt.Errorf("imap: decode qp: %w", err)
		}
		body = decoded
	default:
		// "", "7bit", "8bit", "binary", or anything else: pass through.
		// Unknown encodings are treated as identity rather than failing the
		// download — worst case the user gets a file they can't open, which
		// is no worse than failing outright.
	}
	return body, nil
}

// cteFromMIMEHeaders parses an RFC 822 header block (as returned by
// BODY.PEEK[<part>.MIME]) and returns the lowercase, trimmed value of the
// Content-Transfer-Encoding header, or "" if not present / unparseable.
func cteFromMIMEHeaders(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	tr := textproto.NewReader(bufio.NewReader(bytes.NewReader(b)))
	hdr, err := tr.ReadMIMEHeader()
	if err != nil && err != io.EOF {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(hdr.Get("Content-Transfer-Encoding")))
}

// parsePartPath turns a dotted BODYSTRUCTURE path like "1.2.3" into the
// []int form expected by imap.FetchItemBodySection.Part. An empty string
// returns nil (= request the whole body).
func parsePartPath(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// StoreFlags issues UID STORE +FLAGS / -FLAGS for one UID.
func (c *Client) StoreFlags(ctx context.Context, uid int64, flags []string, add bool) error {
	op := imap.StoreFlagsAdd
	if !add {
		op = imap.StoreFlagsDel
	}
	imapFlags := make([]imap.Flag, 0, len(flags))
	for _, f := range flags {
		imapFlags = append(imapFlags, imap.Flag(f))
	}
	seq := imap.UIDSetNum(imap.UID(uid))
	// As with Fetch, Store dispatches to UID STORE when given a UIDSet.
	// Store returns a *FetchCommand whose Close drains the response and
	// returns the command's terminating error.
	return c.c.Store(seq, &imap.StoreFlags{Op: op, Flags: imapFlags}, nil).Close()
}
