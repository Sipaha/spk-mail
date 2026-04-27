package mime

import (
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
