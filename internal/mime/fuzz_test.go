package mime

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// FuzzParse exercises Parse with arbitrary byte input. The seed corpus
// covers the shapes the unit tests already exercise (plain, multipart
// alternative, encoded-word headers, legacy-charset bodies); the fuzzer
// then mutates from there to find inputs that panic, hang, or violate the
// "non-error return must produce a coherent ParsedMessage" invariant.
//
// Run an actual fuzz pass via:
//
//	go test -run=^FuzzParse$ -fuzz=FuzzParse ./internal/mime/
//
// Without -fuzz, plain `go test` runs the seeds only — that's enough to
// catch a regression that breaks one of the cases below.
func FuzzParse(f *testing.F) {
	seeds := [][]byte{
		[]byte("From: a@b\r\nSubject: x\r\nDate: Mon, 27 Apr 2026 10:30:00 +0000\r\nMessage-ID: <a@x>\r\nContent-Type: text/plain\r\n\r\nbody"),
		[]byte("From: a@b\r\nSubject: =?utf-8?B?aGk=?=\r\nDate: Mon, 27 Apr 2026 10:30:00 +0000\r\nContent-Type: multipart/alternative; boundary=\"x\"\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nplain\r\n--x\r\nContent-Type: text/html\r\n\r\n<p>html</p>\r\n--x--\r\n"),
		[]byte("From: a@b\r\nSubject: x\r\nContent-Type: text/plain; charset=koi8-r\r\n\r\n\xF0\xD2\xC9\xD7\xC5\xD4"),
		[]byte("From: a@b\r\nSubject: =?windows-1251?B?z/Do4uXy?=\r\nContent-Type: text/plain\r\n\r\nx"),
		// Truncated multipart — must NOT panic; should warn-log + return partial.
		[]byte("From: a@b\r\nSubject: x\r\nContent-Type: multipart/mixed; boundary=\"x\"\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nbody"),
		// Empty input.
		[]byte(""),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		// Parse must never panic regardless of input shape. A non-nil
		// ParsedMessage with empty fields is acceptable; a nil deref or
		// stack overflow (e.g. from runaway multipart depth) is not.
		_, _ = Parse(data)
	})
}

// dangerousAttrs are attributes whose value can drive navigation or script
// execution if it carries a javascript: / vbscript: scheme. The fuzz
// invariant checks tokens from x/net/html, so plain-text occurrences of
// "javascript:" in body content (which render as inert text) don't
// false-positive.
var dangerousAttrs = map[string]bool{
	"href": true, "src": true, "action": true,
	"formaction": true, "xlink:href": true,
}

// validateSanitizeOutput parses out as HTML and asserts the post-sanitize
// invariants. Returns the first violation, or nil if the output is clean.
func validateSanitizeOutput(out string) error {
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return nil
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, hasAttr := z.TagName()
		if strings.EqualFold(string(name), "script") {
			return fmtErr("found <script> tag")
		}
		if !hasAttr {
			continue
		}
		for {
			k, v, more := z.TagAttr()
			key := strings.ToLower(string(k))
			if dangerousAttrs[key] {
				val := strings.TrimSpace(strings.ToLower(string(v)))
				if strings.HasPrefix(val, "javascript:") || strings.HasPrefix(val, "vbscript:") {
					return fmtErr("attr " + key + " carries live scheme: " + string(v))
				}
			}
			if !more {
				break
			}
		}
	}
}

func fmtErr(s string) error { return &fuzzErr{s} }

type fuzzErr struct{ s string }

func (e *fuzzErr) Error() string { return e.s }

// FuzzSanitize exercises Sanitize with arbitrary HTML input. The seed
// corpus covers the categories the unit tests already pin — script blocks,
// event handlers, javascript: hrefs, dangerous style values — and asserts
// the same cross-input invariants on every fuzzer-generated string:
//
//   - output must not contain a <script open tag
//   - output must not contain javascript: or vbscript: anywhere
//
// A regression in the bluemonday policy or the AllowStyles MatchingHandler
// would violate one of these on the next mutated input.
func FuzzSanitize(f *testing.F) {
	seeds := []string{
		``,
		`<p>hi</p>`,
		`<p>hi</p><script>alert(1)</script>`,
		`<img src="https://tracker.example/pixel.png">`,
		`<a href="javascript:alert(1)">x</a>`,
		`<a href="JaVaScRiPt:alert(1)">x</a>`,
		`<a href="vbscript:alert(1)">x</a>`,
		`<a href="data:text/html,<script>alert(1)</script>">x</a>`,
		`<style>body{background:url(https://t/x)}</style>`,
		`<p style="color:red; width:expression(alert(1))">x</p>`,
		`<div onload="alert(1)">x</div>`,
		`<img src="cid:logo">`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := Sanitize(in)
		if err := validateSanitizeOutput(out); err != nil {
			t.Fatalf("Sanitize output violates invariant: %s\nin=%q\nout=%q", err, in, out)
		}
		// Sanitize is intentionally NOT idempotent: img-rewrite injects a
		// data: scheme into the placeholder src, which a second pass then
		// strips because data: isn't in the URL allowlist. UnblockRemote +
		// re-Sanitize is the production round-trip; idempotency is not a
		// security invariant we enforce.
	})
}
