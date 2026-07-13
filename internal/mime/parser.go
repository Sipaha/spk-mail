package mime

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/mail"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	gomsg "github.com/emersion/go-message"
	gocharset "github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

// MaxFilenameBytes caps the byte length of a filename component on disk.
// Most Linux/macOS filesystems accept 255 bytes per path component; 200
// leaves headroom for whatever rename(.tmp-XXXXX) dance the writer does
// (fsutil.AtomicWrite uses an 18-byte temp suffix, the longest realistic
// caller suffix we have).
const MaxFilenameBytes = 200

// ErrMIMEDepthExceeded is returned by Parse when a message's multipart
// nesting goes past maxMIMEDepth. Tests use errors.Is for a stable
// contract independent of message wording.
var ErrMIMEDepthExceeded = errors.New("mime: multipart depth exceeds limit")

type ParsedMessage struct {
	MessageID   string
	InReplyTo   string
	References  []string
	Subject     string
	From        string
	To          []string
	Cc          []string
	Date        time.Time
	BodyText    string
	BodyHTML    string
	Attachments []ParsedAttachment
}

type ParsedAttachment struct {
	PartID      string
	Filename    string
	ContentType string
	Size        int64
}

// maxMIMEDepth bounds multipart recursion. A pathological email with many
// nested multiparts could otherwise overflow the parsing goroutine's stack
// (and the StoreWriter goroutine that owns it). 64 is comfortably deeper than
// any sane real-world structure (alternative + related + signed wrapping
// rarely exceeds depth 5).
const maxMIMEDepth = 64

func Parse(raw []byte) (*ParsedMessage, error) {
	r, err := gomsg.Read(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	mr := gomail.NewReader(r)
	hdr := mr.Header

	p := &ParsedMessage{}
	subj, _ := hdr.Subject()
	p.Subject = decodeHeader(subj) // belt-and-braces; emersion decodes most cases but bails on malformed headers
	p.From = headerAddrFirst(hdr, "From")
	p.To = headerAddrAll(hdr, "To")
	p.Cc = headerAddrAll(hdr, "Cc")
	p.Date, _ = hdr.Date()
	p.MessageID = strings.TrimSpace(hdr.Get("Message-ID"))
	p.InReplyTo = strings.TrimSpace(hdr.Get("In-Reply-To"))
	p.References = parseRefs(hdr.Get("References"))

	if err := walk(r, "", p, 0); err != nil {
		return nil, err
	}
	return p, nil
}

func walk(e *gomsg.Entity, partID string, p *ParsedMessage, depth int) error {
	if depth > maxMIMEDepth {
		return fmt.Errorf("%w (depth=%d)", ErrMIMEDepthExceeded, depth)
	}
	mt, params, _ := e.Header.ContentType()
	disp, _, _ := e.Header.ContentDisposition()
	if disp == "attachment" || (params["name"] != "" && !strings.HasPrefix(mt, "text/")) {
		fname := params["name"]
		if cd := e.Header.Get("Content-Disposition"); cd != "" {
			if _, dparams, err := mime.ParseMediaType(cd); err == nil {
				if n := dparams["filename"]; n != "" {
					fname = n
				}
			}
		}
		// safeFilename decodes RFC 2047 names, strips path traversal, and
		// caps byte length so the DB stores the safe form.
		fname = safeFilename(fname)
		// Fallback: many emails carry an inline image or attachment with no
		// filename at all (just Content-Type). Synthesize "att-<partID><ext>"
		// so the downloader has something safe to write — without this the
		// downloader rejects the row as "unsafe filename" and the attachment
		// stays pending forever.
		if strings.TrimSpace(fname) == "" {
			fname = SynthFilename(partID, mt)
		}
		buf, err := io.ReadAll(e.Body)
		if err != nil {
			return fmt.Errorf("mime: read attachment body: %w", err)
		}
		p.Attachments = append(p.Attachments, ParsedAttachment{
			PartID: partID, Filename: fname, ContentType: mt, Size: int64(len(buf)),
		})
		return nil
	}
	if mr := e.MultipartReader(); mr != nil {
		i := 1
		for {
			sub, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				// Malformed boundary / truncated message: surface as a warn so
				// it shows up in the testapi log buffer, but keep whatever
				// parts we already collected — losing the entire message
				// because the last sub-part is corrupt would be worse than a
				// best-effort partial parse.
				slog.Warn("mime: multipart NextPart failed", "part", partID, "err", err)
				return nil
			}
			subID := partID
			if subID != "" {
				subID += "."
			}
			subID += itoa(i)
			if err := walk(sub, subID, p, depth+1); err != nil {
				return err
			}
			i++
		}
	}
	switch {
	case strings.HasPrefix(mt, "text/plain"):
		buf, err := io.ReadAll(e.Body)
		if err != nil {
			return fmt.Errorf("mime: read text/plain body: %w", err)
		}
		if p.BodyText == "" {
			p.BodyText = string(buf)
		}
	case strings.HasPrefix(mt, "text/html"):
		buf, err := io.ReadAll(e.Body)
		if err != nil {
			return fmt.Errorf("mime: read text/html body: %w", err)
		}
		if p.BodyHTML == "" {
			p.BodyHTML = string(buf)
		}
	}
	return nil
}

// wordDecoder decodes RFC 2047 encoded-words (=?charset?B?…?= / =?charset?Q?…?=)
// in header field values. emersion's AddressList sometimes leaves Name fields
// undecoded for malformed-but-common header forms, and falls back to returning
// no addresses at all — in which case the raw `h.Get(key)` value still has the
// encoded-word in it. Using mime.WordDecoder explicitly fixes both cases.
//
// CharsetReader delegates to go-message/charset, which knows the legacy 8-bit
// charsets (koi8-r, windows-1251, iso-8859-*) common in Russian/European mail
// archives. Without this wiring the body parts came through as "unhandled
// charset" parse failures and headers like Subject left their =?…?= form
// raw, breaking display + thread-bucket subject normalization.
var wordDecoder = &mime.WordDecoder{CharsetReader: gocharset.Reader}

// decodeHeader runs s through mime.WordDecoder. On any error returns s
// verbatim. Safe to call on already-decoded text — it's a no-op when there
// are no encoded-word markers.
func decodeHeader(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	if d, err := wordDecoder.DecodeHeader(s); err == nil {
		return d
	}
	return s
}

func headerAddrFirst(h gomail.Header, key string) string {
	addrs, _ := h.AddressList(key)
	if len(addrs) == 0 {
		return decodeHeader(strings.TrimSpace(h.Get(key)))
	}
	a := addrs[0]
	name := decodeHeader(a.Name)
	if name != "" {
		return name + " <" + a.Address + ">"
	}
	return a.Address
}

func headerAddrAll(h gomail.Header, key string) []string {
	addrs, _ := h.AddressList(key)
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		name := decodeHeader(a.Name)
		if name != "" {
			out = append(out, name+" <"+a.Address+">")
		} else {
			out = append(out, a.Address)
		}
	}
	return out
}

func parseRefs(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.Fields(v)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.HasPrefix(p, "<") && strings.HasSuffix(p, ">") {
			out = append(out, p)
		}
	}
	return out
}

// SanitizeFilename strips dangerous and visually deceptive characters from
// an attacker-controlled filename. Removes:
//
//   - Unicode bidirectional override / embedding marks (U+202A..U+202E,
//     U+2066..U+2069) — these can disguise a `.exe` as `.pdf` in any file
//     manager that doesn't render bidi defensively, opening a click-to-run
//     vector through xdg-open.
//   - C0/C1 control characters and DEL.
//   - Directory separators (/ and \) and Unicode lookalikes (fraction
//     slash U+2044, fullwidth solidus U+FF0F).
//   - Leading dots (so an attachment cannot create a hidden dotfile and the
//     downloader can't be tricked into writing `.bashrc`).
//
// Returns "" if the input collapses to empty after stripping; the caller
// should fall back to SynthFilename in that case.
func SanitizeFilename(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		if isDangerousFilenameRune(r) {
			return -1
		}
		return r
	}, name)
	cleaned = strings.TrimLeft(cleaned, ".")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned
}

// isDangerousFilenameRune reports whether r is a bidi/embedding control,
// directory separator, or Unicode separator-lookalike. Listed by codepoint
// rather than as a literal string so the source file stays free of
// invisible-format characters (which staticcheck ST1018 rejects).
func isDangerousFilenameRune(r rune) bool {
	switch r {
	case '/', '\\':
		return true
	case 0x2044: // ⁄ FRACTION SLASH
		return true
	case 0xFF0F: // / FULLWIDTH SOLIDUS
		return true
	case 0x202A, 0x202B, 0x202C, 0x202D, 0x202E: // LRE/RLE/PDF/LRO/RLO
		return true
	case 0x2066, 0x2067, 0x2068, 0x2069: // LRI/RLI/FSI/PDI
		return true
	}
	return false
}

// safeFilename returns a filesystem-safe form of a (possibly attacker- or
// legacy-produced) filename. Pipeline:
//
//  1. decodeHeader — RFC 2047 encoded-words ("=?UTF-8?B?…?="). Necessary
//     for legacy DB rows inserted before the WordDecoder charset wiring
//     (commit 334976a) where Cyrillic koi8-r/windows-1251 names from
//     Outlook calendar invites etc. failed to decode and the raw form
//     ended up in the column.
//  2. filepath.Base — take the leaf component first ("../etc/passwd" →
//     "passwd"). Done BEFORE SanitizeFilename so sanitize sees just the
//     component, not the full traversal string (whose '/' separators
//     would otherwise collapse to no-ops and lose the leaf intent).
//  3. SanitizeFilename — strip bidi-override / control / path-separator
//     runes (executable disguise + extra-paranoia path defence in case
//     filepath.Base left an exotic separator like fullwidth solidus).
//  4. Cap byte length at MaxFilenameBytes, preserving the extension and
//     snapping the truncation to a valid UTF-8 rune boundary so we don't
//     write a half-codepoint to disk.
//
// Returns "" only if the input collapses to empty or a degenerate path
// component (".", "..", "/"); the caller should fall back to SynthFilename.
// Idempotent: passing in an already-safe ASCII filename returns it unchanged.
func safeFilename(raw string) string {
	decoded := decodeHeader(raw)
	cleaned := filepath.Base(decoded)
	cleaned = SanitizeFilename(cleaned)
	switch cleaned {
	case "", ".", "..", "/":
		return ""
	}
	if len(cleaned) <= MaxFilenameBytes {
		return cleaned
	}
	// Truncate while preserving the extension. If the extension itself is
	// pathological (longer than half the cap — e.g. a forged ".docxdocx…")
	// we drop it rather than letting it eat the whole budget.
	ext := filepath.Ext(cleaned)
	if len(ext) > MaxFilenameBytes/2 {
		ext = ""
	}
	keep := MaxFilenameBytes - len(ext)
	base := cleaned[:len(cleaned)-len(ext)]
	if keep > len(base) {
		keep = len(base)
	}
	// Snap back to a UTF-8 rune boundary. utf8.RuneStart returns true for
	// the first byte of each codepoint; backing off one byte at a time is
	// O(4) worst-case (max UTF-8 sequence is 4 bytes).
	for keep > 0 && !utf8.RuneStart(base[keep]) {
		keep--
	}
	return base[:keep] + ext
}

// SynthFilename builds a plausible filename for parts that arrive without
// Content-Disposition filename or Content-Type name. Picks an extension from
// the MIME registry when available, falls back to ".bin". The id makes the
// name unique inside one message so multiple unnamed parts don't collide;
// callers pass either the MIME partID at parse time or the attachment's
// primary key when retro-fitting at download time.
func SynthFilename(id, mt string) string {
	ext := ".bin"
	if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
		ext = exts[0]
	}
	id = strings.ReplaceAll(id, ".", "-")
	if id == "" {
		id = "0"
	}
	return "att-" + id + ext
}

func itoa(n int) string {
	if n < 10 {
		return string('0' + rune(n))
	}
	var b []byte
	for n > 0 {
		b = append([]byte{'0' + byte(n%10)}, b...)
		n /= 10
	}
	return string(b)
}

var _ = mail.Address{} // keep import if a future tweak uses it
