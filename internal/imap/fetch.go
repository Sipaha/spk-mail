package imap

import (
	"context"
	"fmt"
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

// FetchSinceUID streams messages with UID > sinceUID via UID FETCH.
// `body` true = include full RFC822 (used during initial sync); false = envelope only (not used yet).
func (c *Client) FetchSinceUID(ctx context.Context, sinceUID int64, body bool) (<-chan FetchedMessage, <-chan error) {
	out := make(chan FetchedMessage, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		// Build "n:*" UID range. AddRange's stop=0 represents '*'.
		var seq imap.UIDSet
		seq.AddRange(imap.UID(sinceUID+1), 0)

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
// Returns the raw bytes of the section as transmitted by the server (still
// in its Content-Transfer-Encoding form — decoding is the caller's job).
func (c *Client) FetchBodyPart(ctx context.Context, uid int64, partID string) ([]byte, error) {
	seq := imap.UIDSetNum(imap.UID(uid))
	opts := &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{
			{Specifier: imap.PartSpecifierNone, Part: parsePartPath(partID), Peek: true},
		},
	}
	// Fetch dispatches to UID FETCH automatically when numSet is a UIDSet
	// (see imapclient/fetch.go). Mirroring FetchSinceUID's pattern keeps the
	// read-loop drained on early return; FetchCommand.Close is idempotent.
	cmd := c.c.Fetch(seq, opts)
	defer cmd.Close()

	var buf []byte
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
		for _, sec := range data.BodySection {
			if len(sec.Bytes) > 0 {
				buf = sec.Bytes
				break
			}
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, err
	}
	if buf == nil {
		return nil, fmt.Errorf("imap: no body part %q for uid %d", partID, uid)
	}
	return buf, nil
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
