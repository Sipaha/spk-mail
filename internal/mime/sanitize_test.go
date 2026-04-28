package mime

import (
	"testing"

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
