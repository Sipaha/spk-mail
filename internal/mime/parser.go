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
	p.Subject, _ = hdr.Subject()
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

func headerAddrFirst(h gomail.Header, key string) string {
	addrs, _ := h.AddressList(key)
	if len(addrs) == 0 {
		return strings.TrimSpace(h.Get(key))
	}
	a := addrs[0]
	if a.Name != "" {
		return a.Name + " <" + a.Address + ">"
	}
	return a.Address
}

func headerAddrAll(h gomail.Header, key string) []string {
	addrs, _ := h.AddressList(key)
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Name != "" {
			out = append(out, a.Name+" <"+a.Address+">")
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
