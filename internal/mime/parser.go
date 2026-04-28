package mime

import (
	"bytes"
	"io"
	"mime"
	"net/mail"
	"strings"
	"time"

	gomsg "github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
)

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

	walk(r, "", p)
	return p, nil
}

func walk(e *gomsg.Entity, partID string, p *ParsedMessage) {
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
		// RFC 2047 encoded names ("=?utf-8?B?...?=") happen for non-ASCII
		// filenames; ParseMediaType doesn't decode them on its own.
		fname = decodeHeader(fname)
		// Fallback: many emails carry an inline image or attachment with no
		// filename at all (just Content-Type). Synthesize "att-<partID><ext>"
		// so the downloader has something safe to write — without this the
		// downloader rejects the row as "unsafe filename" and the attachment
		// stays pending forever.
		if strings.TrimSpace(fname) == "" {
			fname = SynthFilename(partID, mt)
		}
		buf, _ := io.ReadAll(e.Body)
		p.Attachments = append(p.Attachments, ParsedAttachment{
			PartID: partID, Filename: fname, ContentType: mt, Size: int64(len(buf)),
		})
		return
	}
	if mr := e.MultipartReader(); mr != nil {
		i := 1
		for {
			sub, err := mr.NextPart()
			if err != nil {
				return
			}
			subID := partID
			if subID != "" {
				subID += "."
			}
			subID += itoa(i)
			walk(sub, subID, p)
			i++
		}
	}
	switch {
	case strings.HasPrefix(mt, "text/plain"):
		buf, _ := io.ReadAll(e.Body)
		if p.BodyText == "" {
			p.BodyText = string(buf)
		}
	case strings.HasPrefix(mt, "text/html"):
		buf, _ := io.ReadAll(e.Body)
		if p.BodyHTML == "" {
			p.BodyHTML = string(buf)
		}
	}
}

// wordDecoder decodes RFC 2047 encoded-words (=?charset?B?…?= / =?charset?Q?…?=)
// in header field values. emersion's AddressList sometimes leaves Name fields
// undecoded for malformed-but-common header forms, and falls back to returning
// no addresses at all — in which case the raw `h.Get(key)` value still has the
// encoded-word in it. Using mime.WordDecoder explicitly fixes both cases.
//
// CharsetReader is intentionally nil: stdlib supports UTF-8 (the common case
// for modern email); foreign charsets like windows-1251 fall through and the
// caller sees the raw encoded-word, which is acceptable degradation versus
// crashing on import.
var wordDecoder = new(mime.WordDecoder)

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
