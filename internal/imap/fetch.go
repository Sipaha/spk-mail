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
//
// HighestModSeq is the server's per-mailbox CONDSTORE counter (RFC 7162):
// a monotonically-increasing version number that bumps on ANY metadata
// change to ANY message in the mailbox (flag flip, store, append). The
// sync layer persists it as a watermark and feeds it back as
// CHANGEDSINCE on subsequent FETCHes — the server returns deltas only,
// so a `Прочитано` propagation is O(changed messages) instead of O(N).
//
// Zero means the server didn't return HIGHESTMODSEQ — either it doesn't
// support CONDSTORE, or we didn't ask for it (CondStore=false in
// SelectOptions). Callers fall back to no flag-delta sync in that case
// rather than guess.
type FolderState struct {
	UIDValidity   int64
	UIDNext       int64
	Exists        int64
	HighestModSeq uint64
}

// Select issues a SELECT for the given mailbox, opting into CONDSTORE
// when the server advertises the capability. The CondStore option is
// gated because RFC 7162 requires it: sending CONDSTORE to a server
// that doesn't advertise it is a CLIENTBUG protocol error, not a
// silent no-op. Servers without CONDSTORE return HighestModSeq=0,
// which the sync layer treats as "skip flag-delta sync" — see
// FolderState's doc.
func (c *Client) Select(_ context.Context, mailbox string) (FolderState, error) {
	var opts *imap.SelectOptions
	if c.HasCondStore() {
		opts = &imap.SelectOptions{CondStore: true}
	}
	sel, err := c.c.Select(mailbox, opts).Wait()
	if err != nil {
		return FolderState{}, err
	}
	return FolderState{
		UIDValidity:   int64(sel.UIDValidity),
		UIDNext:       int64(sel.UIDNext),
		Exists:        int64(sel.NumMessages),
		HighestModSeq: sel.HighestModSeq,
	}, nil
}

// UIDsAbove returns the actual UIDs present in the currently-selected
// mailbox whose value is strictly greater than `sinceUID`. Issues a
// `UID SEARCH UID start:*` and filters the response client-side.
//
// IMAP range semantics (RFC 3501 9.6 / 6.4.4) treat `start:*` where
// start > current-max as equivalent to `current-max:start`, i.e. it
// matches the highest UID currently in the mailbox even though that
// UID is BELOW our threshold. Yandex faithfully implements this.
// Without the post-search filter, `UIDsAbove(91266)` against a
// mailbox whose max UID is 91266 would return [91266] — the very
// UID we already have — and the caller would either re-fetch it
// or, worse, derive a wrong upper bound and miss the real new UID
// later.
//
// Filter inclusively (uid > sinceUID) so the function name's
// promise actually holds.
func (c *Client) UIDsAbove(ctx context.Context, sinceUID int64) ([]int64, error) {
	var rng imap.UIDSet
	rng.AddRange(imap.UID(sinceUID+1), 0) // 0 = '*'
	criteria := &imap.SearchCriteria{UID: []imap.UIDSet{rng}}
	cmd := c.c.UIDSearch(criteria, nil)
	data, err := cmd.Wait()
	if err != nil {
		return nil, err
	}
	uids, ok := data.All.(imap.UIDSet)
	if !ok || len(uids) == 0 {
		return nil, nil
	}
	out := make([]int64, 0, 16)
	for _, r := range uids {
		stop := r.Stop
		if stop == 0 {
			// "Up to highest" without an explicit number — shouldn't
			// happen for SEARCH responses, but skip rather than loop
			// to infinity.
			continue
		}
		for u := r.Start; u <= stop; u++ {
			if int64(u) > sinceUID {
				out = append(out, int64(u))
			}
		}
	}
	return out, nil
}

// FetchByUIDs streams full message data for the explicit UID set.
// Used after UIDsAbove identifies the actual live UIDs we still need
// to fetch — bypasses UIDNEXT-derived ranges and the eventual-
// consistency / range-collapse quirks they invite.
func (c *Client) FetchByUIDs(ctx context.Context, uids []int64) (<-chan FetchedMessage, <-chan error) {
	out := make(chan FetchedMessage, 16)
	errCh := make(chan error, 1)
	if len(uids) == 0 {
		close(out)
		close(errCh)
		return out, errCh
	}
	go func() {
		defer close(out)
		defer close(errCh)
		var seq imap.UIDSet
		for _, u := range uids {
			seq.AddNum(imap.UID(u))
		}
		opts := &imap.FetchOptions{
			Flags:        true,
			InternalDate: true,
			UID:          true,
			BodySection: []*imap.FetchItemBodySection{
				{Specifier: imap.PartSpecifierNone, Peek: true},
			},
		}
		cmd := c.c.Fetch(seq, opts)
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
			fm := FetchedMessage{UID: int64(data.UID), Internal: data.InternalDate.Unix()}
			for _, f := range data.Flags {
				fm.Flags = append(fm.Flags, string(f))
			}
			for _, sec := range data.BodySection {
				if len(sec.Bytes) > 0 {
					fm.Raw = sec.Bytes
					break
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

// FlagDelta is one row of a CHANGEDSINCE flag-delta sweep: the UID of
// a message whose metadata changed since the caller's watermark, plus
// the new flag set as the server sees it.
type FlagDelta struct {
	UID   int64
	Flags []string
}

// FetchFlagsChangedSince streams (uid, flags) pairs for every message
// in the currently-selected mailbox whose CONDSTORE MODSEQ is greater
// than `sinceModSeq`. Issues:
//
//	UID FETCH 1:* (FLAGS UID) (CHANGEDSINCE <sinceModSeq>)
//
// Server-side filter — we never see messages that didn't change.
//
// Caller is responsible for having selected the mailbox with
// CondStore=true; otherwise the server returns BAD on CHANGEDSINCE.
func (c *Client) FetchFlagsChangedSince(ctx context.Context, sinceModSeq uint64) (<-chan FlagDelta, <-chan error) {
	out := make(chan FlagDelta, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		var seq imap.UIDSet
		seq.AddRange(1, 0) // 1:* — server does the modseq filter
		opts := &imap.FetchOptions{
			Flags:        true,
			UID:          true,
			ChangedSince: sinceModSeq,
		}
		cmd := c.c.Fetch(seq, opts)
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
			fd := FlagDelta{UID: int64(data.UID)}
			for _, f := range data.Flags {
				fd.Flags = append(fd.Flags, string(f))
			}
			select {
			case out <- fd:
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

// StoreFlags issues UID STORE +FLAGS / -FLAGS for one or more UIDs on the
// currently-selected mailbox. The caller is responsible for SELECTing the
// right folder before calling. An empty uids slice is a no-op.
//
// The underlying go-imap Store call is synchronous and does not honour ctx
// mid-flight, so we only short-circuit on cancellation before issuing the
// command — enough to skip a STORE when the worker shutdown signal already
// fired but the drain loop hasn't observed it.
func (c *Client) StoreFlags(ctx context.Context, uids []int64, flags []string, add bool) error {
	if len(uids) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	op := imap.StoreFlagsAdd
	if !add {
		op = imap.StoreFlagsDel
	}
	imapFlags := make([]imap.Flag, 0, len(flags))
	for _, f := range flags {
		imapFlags = append(imapFlags, imap.Flag(f))
	}
	imapUIDs := make([]imap.UID, len(uids))
	for i, u := range uids {
		imapUIDs[i] = imap.UID(u)
	}
	seq := imap.UIDSetNum(imapUIDs...)
	return c.c.Store(seq, &imap.StoreFlags{Op: op, Flags: imapFlags}, nil).Close()
}
