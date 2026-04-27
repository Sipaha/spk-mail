package mockimap

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"gopkg.in/yaml.v3"
)

// Fixture is the top-level container for a test fixture YAML file.
type Fixture struct {
	Accounts []FixtureAccount `yaml:"accounts" json:"accounts"`
}

// FixtureAccount describes one mail account with its folders and messages.
type FixtureAccount struct {
	Name     string          `yaml:"name"     json:"name"`
	Email    string          `yaml:"email"    json:"email"`
	Password string          `yaml:"password" json:"password"`
	Color    string          `yaml:"color"    json:"color"`
	UseMock  bool            `yaml:"use_mock" json:"use_mock"`
	Folders  []FixtureFolder `yaml:"folders"  json:"folders"`
}

// FixtureFolder describes a mailbox folder and its messages.
type FixtureFolder struct {
	Name     string           `yaml:"name"     json:"name"`
	Messages []FixtureMessage `yaml:"messages" json:"messages"`
}

// FixtureMessage describes a single message to be appended into a folder.
type FixtureMessage struct {
	From        string              `yaml:"from"        json:"from"`
	To          []string            `yaml:"to"          json:"to"`
	Subject     string              `yaml:"subject"     json:"subject"`
	Date        time.Time           `yaml:"date"        json:"date"`
	BodyText    string              `yaml:"body_text"   json:"body_text"`
	BodyHTML    string              `yaml:"body_html"   json:"body_html"`
	Flags       []string            `yaml:"flags"       json:"flags"`
	Attachments []FixtureAttachment `yaml:"attachments" json:"attachments"`
}

// FixtureAttachment describes an attachment to be included in a message.
type FixtureAttachment struct {
	Filename    string `yaml:"filename"     json:"filename"`
	ContentType string `yaml:"content_type" json:"content_type"`
	Size        int    `yaml:"size"         json:"size"`
}

// LoadFixture reads a YAML fixture file from disk and unmarshals it.
func LoadFixture(path string) (*Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mockimap: load fixture %s: %w", path, err)
	}
	var f Fixture
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("mockimap: parse fixture %s: %w", path, err)
	}
	return &f, nil
}

// NowSource abstracts a clock so fixtures can be seeded against either
// time.Now (production / tests that don't care) or a swappable *clock.Clock
// (browser-mode test screenshots that need deterministic timestamps).
//
// Pass nil to ApplyWithClock to use the real wall clock.
type NowSource interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// Apply seeds the server with all accounts, folders, and messages described
// in the fixture, using the real wall clock for any zero-date defaults.
func (s *Server) Apply(f *Fixture) error {
	return s.ApplyWithClock(f, wallClock{})
}

// ApplyWithClock is like Apply but resolves zero-date message defaults
// against the provided clock instead of time.Now.
func (s *Server) ApplyWithClock(f *Fixture, now NowSource) error {
	if now == nil {
		now = wallClock{}
	}
	for _, acc := range f.Accounts {
		password := acc.Password
		if password == "" {
			password = "secret"
		}

		if err := s.AddUser(acc.Email, password); err != nil {
			return fmt.Errorf("mockimap: add user %s: %w", acc.Email, err)
		}

		u := s.User(acc.Email)
		if u == nil {
			return fmt.Errorf("mockimap: user %s not found after add", acc.Email)
		}

		for _, folder := range acc.Folders {
			// INBOX is created automatically; create other folders.
			if !strings.EqualFold(folder.Name, "INBOX") {
				if err := u.Create(folder.Name, nil); err != nil {
					if !isAlreadyExists(err) {
						return fmt.Errorf("mockimap: create folder %s for %s: %w", folder.Name, acc.Email, err)
					}
				}
			}

			for _, msg := range folder.Messages {
				// Resolve zero-date here so buildRFC822/buildHeaders don't need
				// the clock and always see a concrete timestamp.
				if msg.Date.IsZero() {
					msg.Date = now.Now()
				}

				raw := buildRFC822(msg)
				lr := &literalBytes{data: raw}

				opts := &imap.AppendOptions{Time: msg.Date}

				if _, err := u.Append(folder.Name, lr, opts); err != nil {
					return fmt.Errorf("mockimap: append message to %s/%s: %w", acc.Email, folder.Name, err)
				}
			}
		}
	}
	return nil
}

// isAlreadyExists returns true if err indicates the mailbox already exists.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "AlreadyExists")
}

// buildRFC822 composes a minimal RFC 822 message from a fixture message.
func buildRFC822(m FixtureMessage) []byte {
	if len(m.Attachments) > 0 {
		return buildMultipart(m)
	}
	return buildSimple(m)
}

func buildHeaders(m FixtureMessage) string {
	// Callers (ApplyWithClock) are expected to resolve zero-dates before
	// invoking the build chain; if they don't, fall back to wallClock so the
	// header is still valid.
	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}
	msgID := fmt.Sprintf("<%d.%s@mockimap>",
		date.UnixNano(),
		strings.Map(func(r rune) rune {
			if r == ' ' {
				return '-'
			}
			return r
		}, m.Subject),
	)
	var sb strings.Builder
	sb.WriteString("From: " + m.From + "\r\n")
	sb.WriteString("To: " + strings.Join(m.To, ", ") + "\r\n")
	sb.WriteString("Subject: " + m.Subject + "\r\n")
	sb.WriteString("Date: " + date.Format(time.RFC1123Z) + "\r\n")
	sb.WriteString("Message-ID: " + msgID + "\r\n")
	return sb.String()
}

func buildSimple(m FixtureMessage) []byte {
	var buf bytes.Buffer
	buf.WriteString(buildHeaders(m))
	if m.BodyHTML != "" {
		buf.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		buf.WriteString("MIME-Version: 1.0\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(m.BodyHTML)
	} else {
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		buf.WriteString("MIME-Version: 1.0\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(m.BodyText)
	}
	return buf.Bytes()
}

func buildMultipart(m FixtureMessage) []byte {
	// We write parts into buf first, then prepend the outer headers.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	outerHeaders := buildHeaders(m)
	outerHeaders += "MIME-Version: 1.0\r\n"
	outerHeaders += "Content-Type: multipart/mixed; boundary=\"" + mw.Boundary() + "\"\r\n"
	outerHeaders += "\r\n"

	// Text body part.
	partHeaders := make(textproto.MIMEHeader)
	if m.BodyHTML != "" {
		partHeaders.Set("Content-Type", "text/html; charset=utf-8")
	} else {
		partHeaders.Set("Content-Type", "text/plain; charset=utf-8")
	}
	partHeaders.Set("Content-Transfer-Encoding", "7bit")

	pw, err := mw.CreatePart(partHeaders)
	if err == nil {
		body := m.BodyText
		if m.BodyHTML != "" {
			body = m.BodyHTML
		}
		_, _ = pw.Write([]byte(body))
	}

	// Attachment parts.
	for _, att := range m.Attachments {
		ct := att.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		ah := make(textproto.MIMEHeader)
		ah.Set("Content-Type", ct)
		ah.Set("Content-Transfer-Encoding", "base64")
		ah.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", att.Filename))

		ap, err2 := mw.CreatePart(ah)
		if err2 == nil {
			payload := bytes.Repeat([]byte{0}, att.Size)
			enc := base64.StdEncoding.EncodeToString(payload)
			_, _ = ap.Write([]byte(enc))
		}
	}

	_ = mw.Close()

	var out bytes.Buffer
	out.WriteString(outerHeaders)
	out.Write(buf.Bytes())
	return out.Bytes()
}

// literalBytes implements imap.LiteralReader over a []byte.
type literalBytes struct {
	data   []byte
	reader *bytes.Reader
}

func (l *literalBytes) Read(p []byte) (int, error) {
	if l.reader == nil {
		l.reader = bytes.NewReader(l.data)
	}
	return l.reader.Read(p)
}

func (l *literalBytes) Size() int64 {
	return int64(len(l.data))
}
