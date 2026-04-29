package mime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSanitize_StripsScript(t *testing.T) {
	in := `<p>hi</p><script>alert(1)</script>`
	out := Sanitize(in)
	require.NotContains(t, out, "<script")
	require.Contains(t, out, "<p>hi</p>")
}

func TestSanitize_RewritesRemoteImages(t *testing.T) {
	in := `<img src="https://tracker.example/pixel.png">`
	out := Sanitize(in)
	require.NotContains(t, out, ` src="https://`)
	require.Contains(t, out, `data-spk-original-src="https://tracker.example/pixel.png"`)
	require.Contains(t, out, `src="data:image/svg+xml`) // placeholder
}

func TestSanitize_AllowsCidImage(t *testing.T) {
	in := `<img src="cid:logo">`
	out := Sanitize(in)
	require.Contains(t, out, `src="cid:logo"`)
}

func TestUnblockRemote_RestoresSrc(t *testing.T) {
	in := `<img src="data:..." data-spk-original-src="https://x/y.png">`
	out := UnblockRemote(in)
	require.Contains(t, out, `src="https://x/y.png"`)
	require.NotContains(t, out, `data-spk-original-src`)
}

func TestSanitize_StripsEventHandlers(t *testing.T) {
	cases := []string{
		`<img src="cid:logo" onerror="alert(1)">`,
		`<a onclick="alert(1)" href="https://x/y">click</a>`,
		`<div onload="alert(1)">x</div>`,
	}
	for _, in := range cases {
		out := Sanitize(in)
		require.NotContainsf(t, out, "onerror", "in=%s", in)
		require.NotContainsf(t, out, "onclick", "in=%s", in)
		require.NotContainsf(t, out, "onload", "in=%s", in)
	}
}

func TestSanitize_StripsJavascriptHref(t *testing.T) {
	in := `<a href="javascript:alert(1)">click</a>`
	out := Sanitize(in)
	require.NotContains(t, out, "javascript:")
}

func TestSanitize_StripsJavascriptHref_Variants(t *testing.T) {
	// bluemonday's URL-scheme allowlist should reject mixed-case and
	// leading-whitespace forms too. Pin this so a future regression in the
	// scheme parser doesn't quietly let one of these through.
	cases := []string{
		`<a href="JAVASCRIPT:alert(1)">x</a>`,
		`<a href=" javascript:alert(1)">x</a>`,
		`<a href="JaVaScRiPt:alert(1)">x</a>`,
		`<a href="vbscript:alert(1)">x</a>`,
	}
	for _, in := range cases {
		out := Sanitize(in)
		require.NotContainsf(t, out, "alert(1)", "in=%s out=%s", in, out)
		require.NotRegexpf(t, "(?i)javascript:|vbscript:", out, "in=%s out=%s", in, out)
	}
}

func TestSanitize_StripsDataURIInHref(t *testing.T) {
	// data: hrefs are intentionally not in the URL allowlist; opening a
	// data:text/html link via the system browser would run HTML/JS without
	// our sandbox iframe.
	in := `<a href="data:text/html,<script>alert(1)</script>">click</a>`
	out := Sanitize(in)
	require.NotContains(t, out, `href="data:`)
}

func TestSanitize_StripsStyleBlocks(t *testing.T) {
	// <style> blocks are dropped: bluemonday does not parse the CSS body so
	// url() / @import inside would fire as live network requests, bypassing
	// the remote-image gate that only rewrites <img src>.
	in := `<style>body { background: url(https://tracker.example/pixel) }</style><p>hi</p>`
	out := Sanitize(in)
	require.NotContains(t, out, "<style")
	require.NotContains(t, out, "tracker.example")
	require.Contains(t, out, "<p>hi</p>")
}

func TestSanitize_StripsUrlInInlineStyle(t *testing.T) {
	// background:url() in an inline style attribute is a known tracking
	// vector — the AllowStyles MatchingHandler must drop the whole property.
	in := `<p style="color:red; background-image:url(https://tracker.example/pixel)">hi</p>`
	out := Sanitize(in)
	require.NotContains(t, out, "tracker.example")
	require.NotContains(t, out, "url(")
}

func TestSanitize_KeepsSafeInlineStyle(t *testing.T) {
	in := `<p style="color:red; font-size:14px">hi</p>`
	out := Sanitize(in)
	// bluemonday re-emits styles with a single space after the colon, so we
	// match the canonical form rather than the input.
	require.Contains(t, out, "color: red")
	require.Contains(t, out, "font-size: 14px")
}

// TestSanitize_StripsDangerousStyleValues pins each branch of the AllowStyles
// MatchingHandler explicitly. The url() branch is already exercised by
// TestSanitize_StripsUrlInInlineStyle; the others (expression, behavior,
// @import, javascript:) had only indirect coverage and a regression in any
// one branch would silently re-open a side-channel into network/script.
func TestSanitize_StripsDangerousStyleValues(t *testing.T) {
	// Each input contains one safe property + one dangerous property; the
	// safe one must survive (proves bluemonday parsed the style block) and
	// the dangerous one must be dropped (proves the handler rejected it).
	cases := []struct {
		name        string
		input       string
		mustNotHave string
	}{
		{"expression()", `<p style="color:red; width:expression(alert(1))">x</p>`, "expression"},
		{"behavior",     `<p style="color:red; behavior:url(#default#VML)">x</p>`, "behavior"},
		{"@import",      `<p style="color:red; background:@import url(https://x/y.css)">x</p>`, "@import"},
		{"javascript:",  `<p style="color:red; background:javascript:alert(1)">x</p>`, "javascript:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Sanitize(tc.input)
			require.NotContainsf(t, strings.ToLower(out), tc.mustNotHave, "out=%s", out)
			require.Containsf(t, out, "color: red", "safe property dropped — handler regressed; out=%s", out)
		})
	}
}

// TestSafeFilename_DecodesLegacyRFC2047 verifies that filenames stored in
// raw RFC 2047 encoded-word form (legacy DB rows from before the
// koi8-r/windows-1251 charset reader was wired into WordDecoder) get
// decoded to their actual UTF-8 form when the downloader retries them.
func TestSafeFilename_DecodesLegacyRFC2047(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "windows-1251 calendar invite name",
			raw:  `=?windows-1251?B?0cXMyM3A0CDPziDBxMQg0tDFzcjNwyDPziDTz9DAwsvFzcjeINDI0crA?= =?windows-1251?B?zMggws4gwtDFzN8gws7GxMXNyN8gwiDO0cXNzcUtx8jMzcjJIC5pY3M=?=`,
			want: `СЕМИНАР ПО БДД ТРЕНИНГ ПО УПРАВЛЕНИЮ РИСКАМИ ВО ВРЕМЯ ВОЖДЕНИЯ В ОСЕННЕ-ЗИМНИЙ .ics`,
		},
		{
			name: "UTF-8 base64 multi-chunk",
			raw:  `=?UTF-8?B?0KHQntCfINCyINCh0K3QlCBCRFMuMDItMjgtMDAyLTAwMSDQn9GA0LjQu9C+0LbQtdC90LjQtSA1Lg==?= =?UTF-8?B?b2N4?=`,
			want: `СОП в СЭД BDS.02-28-002-001 Приложение 5.ocx`,
		},
		{
			name: "already-decoded ASCII passes through unchanged",
			raw:  `image001.png`,
			want: `image001.png`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, SafeFilename(tc.raw))
		})
	}
}

// TestSafeFilename_TruncatesPreservingExtension proves the byte-length cap
// keeps the file extension intact (so .docx stays .docx) and snaps to a
// UTF-8 rune boundary so we never write a partial codepoint.
func TestSafeFilename_TruncatesPreservingExtension(t *testing.T) {
	// Build a 250-byte Cyrillic base + ".docx" = ~260 bytes raw.
	base := strings.Repeat("П", 125) // 250 bytes (П = 2 bytes in UTF-8)
	in := base + ".docx"
	got := SafeFilename(in)
	require.LessOrEqual(t, len(got), MaxFilenameBytes)
	require.True(t, strings.HasSuffix(got, ".docx"), "extension must survive truncation")
	require.True(t, len(got) > len(".docx"), "truncated name must keep some of the base")
	// Ensure the truncated string is valid UTF-8 (no half-codepoint at the cut).
	require.True(t, utf8.ValidString(got), "truncated name must be valid UTF-8")
}

// TestSafeFilename_StripsPathTraversal confirms filepath.Base keeps a
// "../etc/passwd" attempt confined to "passwd" — defence-in-depth even
// though SanitizeFilename strips '/' itself.
func TestSafeFilename_StripsPathTraversal(t *testing.T) {
	require.Equal(t, "passwd", SafeFilename(`/etc/passwd`))
	require.Equal(t, "passwd", SafeFilename(`../../etc/../passwd`))
}

// TestSafeFilename_ReturnsEmptyForDegenerate covers the "caller should fall
// back to SynthFilename" contract.
func TestSafeFilename_ReturnsEmptyForDegenerate(t *testing.T) {
	require.Equal(t, "", SafeFilename(""))
	require.Equal(t, "", SafeFilename("."))
	require.Equal(t, "", SafeFilename(".."))
	require.Equal(t, "", SafeFilename("/"))
	require.Equal(t, "", SafeFilename(".....")) // trim-leading-dot collapses to empty
}

