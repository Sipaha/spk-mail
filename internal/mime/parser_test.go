package mime

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse_PlainText(t *testing.T) {
	raw := strings.Join([]string{
		"From: Bob <b@x.y>", "To: Alice <a@x.y>", "Subject: Hello",
		"Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"Message-ID: <abc@x.y>", "Content-Type: text/plain; charset=utf-8", "", "hi there",
	}, "\r\n")
	p, err := Parse([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, "Hello", p.Subject)
	require.Equal(t, "<abc@x.y>", p.MessageID)
	require.Equal(t, "hi there", strings.TrimSpace(p.BodyText))
	require.Empty(t, p.BodyHTML)
}

func TestParse_HTMLAlternative(t *testing.T) {
	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: x", "Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="bnd"`,
		"", "--bnd", "Content-Type: text/plain", "", "plain part",
		"--bnd", "Content-Type: text/html", "", "<p>html part</p>",
		"--bnd--",
	}, "\r\n")
	p, err := Parse([]byte(raw))
	require.NoError(t, err)
	require.Contains(t, p.BodyText, "plain part")
	require.Contains(t, p.BodyHTML, "<p>html part</p>")
}

func TestParse_References(t *testing.T) {
	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: x", "Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"Message-ID: <c@x>", "In-Reply-To: <b@x>",
		"References: <a@x> <b@x>",
		"Content-Type: text/plain", "", "x",
	}, "\r\n")
	p, _ := Parse([]byte(raw))
	require.Equal(t, "<b@x>", p.InReplyTo)
	require.Equal(t, []string{"<a@x>", "<b@x>"}, p.References)
}

func TestParse_AttachmentMetadata(t *testing.T) {
	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: x", "Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="m"`,
		"", "--m", "Content-Type: text/plain", "", "body",
		"--m", `Content-Type: application/pdf; name="report.pdf"`,
		`Content-Disposition: attachment; filename="report.pdf"`,
		"Content-Transfer-Encoding: base64", "", "JVBERi0xLjQK", "--m--",
	}, "\r\n")
	p, err := Parse([]byte(raw))
	require.NoError(t, err)
	require.Len(t, p.Attachments, 1)
	require.Equal(t, "report.pdf", p.Attachments[0].Filename)
	require.Equal(t, "application/pdf", p.Attachments[0].ContentType)
	require.NotEmpty(t, p.Attachments[0].PartID)
}

func TestParse_AttachmentNoFilename(t *testing.T) {
	// Content-Disposition: attachment with no filename and no name= param —
	// SynthFilename must produce a stable "att-<partID>.<ext>" so the
	// downloader doesn't reject it as "unsafe filename" and leave it pending.
	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: x", "Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="m"`,
		"", "--m", "Content-Type: text/plain", "", "body",
		"--m", "Content-Type: image/png",
		"Content-Disposition: attachment",
		"Content-Transfer-Encoding: base64", "", "iVBORw0KGgo=", "--m--",
	}, "\r\n")
	p, err := Parse([]byte(raw))
	require.NoError(t, err)
	require.Len(t, p.Attachments, 1)
	require.NotEmpty(t, p.Attachments[0].Filename)
	require.True(t, strings.HasPrefix(p.Attachments[0].Filename, "att-"),
		"synthesised filename should start with att-, got %q", p.Attachments[0].Filename)
}

func TestParse_DepthLimit(t *testing.T) {
	// Build a pathologically nested multipart message: each level wraps the
	// previous in a multipart/mixed. Depth > maxMIMEDepth must surface as a
	// parse error, not crash or silently truncate.
	depth := maxMIMEDepth + 5
	var sb strings.Builder
	sb.WriteString("From: B <b@x>\r\nSubject: x\r\nDate: Mon, 27 Apr 2026 10:30:00 +0000\r\nMIME-Version: 1.0\r\n")
	for i := 0; i < depth; i++ {
		sb.WriteString("Content-Type: multipart/mixed; boundary=\"b" + strings.Repeat("x", i+1) + "\"\r\n\r\n")
		sb.WriteString("--b" + strings.Repeat("x", i+1) + "\r\n")
	}
	sb.WriteString("Content-Type: text/plain\r\n\r\nx\r\n")
	for i := depth - 1; i >= 0; i-- {
		sb.WriteString("--b" + strings.Repeat("x", i+1) + "--\r\n")
	}
	_, err := Parse([]byte(sb.String()))
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrMIMEDepthExceeded),
		"expected ErrMIMEDepthExceeded, got %v", err)
}

func TestSanitizeFilename(t *testing.T) {
	// Use \u escapes for invisible-format codepoints so the source file
	// stays free of literal bidi controls (staticcheck ST1018).
	const rlo = "\u202e"        // RIGHT-TO-LEFT OVERRIDE
	const lro = "\u202d"        // LEFT-TO-RIGHT OVERRIDE
	const fracSlash = "\u2044"  // FRACTION SLASH
	const fullwidthSolidus = "\uff0f"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ASCII passes through", "report.pdf", "report.pdf"},
		{"strips bidi RTL override (U+202E)", "report" + rlo + "gdp.exe", "reportgdp.exe"},
		{"strips bidi LTR override (U+202D)", "doc" + lro + ".exe", "doc.exe"},
		{"strips fraction slash", "fake" + fracSlash + "path.pdf", "fakepath.pdf"},
		{"strips fullwidth solidus", "fake" + fullwidthSolidus + "path.pdf", "fakepath.pdf"},
		{"strips backslash", "fake\\path.pdf", "fakepath.pdf"},
		{"strips forward slash", "fake/path.pdf", "fakepath.pdf"},
		{"strips C0 control chars", "doc\x00\x01.pdf", "doc.pdf"},
		{"strips DEL", "doc\x7f.pdf", "doc.pdf"},
		{"strips leading dots", "...bashrc", "bashrc"},
		{"empty stays empty", "", ""},
		{"only-dots collapses to empty", "....", ""},
		{"unicode that's safe stays", "Привет.pdf", "Привет.pdf"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, SanitizeFilename(c.in))
		})
	}
}
