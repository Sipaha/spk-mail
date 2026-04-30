package mime

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

var (
	policy         *bluemonday.Policy
	imgSrcPattern  = regexp.MustCompile(`(?i)<img\b([^>]*?)\bsrc\s*=\s*"([^"]+)"`)
	dataAttrPat    = regexp.MustCompile(`\sdata-spk-original-src="([^"]+)"`)
	imgFullPattern = regexp.MustCompile(`(?i)<img\b([^>]*)>`)
	srcAttrPat     = regexp.MustCompile(`(?i)\ssrc\s*=\s*"[^"]*"`)
)

const placeholderSVG = `data:image/svg+xml;utf8,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='1'%20height='1'/%3E`

func init() {
	p := bluemonday.UGCPolicy()
	// `<style>` blocks are intentionally NOT allowed: bluemonday's UGC
	// baseline already drops them, and url()/@import inside CSS would
	// otherwise fire as live network requests, bypassing the remote-image
	// gate that only rewrites `<img src>`. Inline `style` attrs are passed
	// through a per-property allowlist that excludes any value calling
	// url() or expression(), so background-image trackers cannot leak the
	// reader's IP through CSS either.
	p.AllowStyles(
		"color", "background-color", "background", "font", "font-family",
		"font-size", "font-weight", "font-style", "text-align", "text-decoration",
		"line-height", "letter-spacing", "vertical-align", "white-space",
		"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
		"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
		"border", "border-top", "border-right", "border-bottom", "border-left",
		"border-color", "border-style", "border-width", "border-radius",
		"width", "height", "min-width", "min-height", "max-width", "max-height",
		"display", "visibility", "overflow", "float", "clear",
	).MatchingHandler(func(value string) bool {
		// Reject any property value that could trigger network fetches or
		// script execution: url(), @import, expression(), behavior, javascript:.
		v := strings.ToLower(value)
		if strings.Contains(v, "url(") ||
			strings.Contains(v, "@import") ||
			strings.Contains(v, "expression(") ||
			strings.Contains(v, "behavior") ||
			strings.Contains(v, "javascript:") {
			return false
		}
		return true
	}).Globally()
	p.AllowAttrs("class").Globally()
	// `data:` scheme is intentionally NOT allowed. Inline images arrive as
	// CID parts in proper MIME structures; allowing data: would let an
	// email embed `<a href="data:text/html,...">` that, when opened via
	// the system browser, runs HTML/JS without our sandbox iframe.
	p.AllowURLSchemes("http", "https", "mailto", "cid")
	policy = p
}

// Sanitize cleans untrusted HTML and rewrites <img src="http(s)://..."> into a
// placeholder + data-spk-original-src so the UI can offer "show remote content".
// Already-cid: images are left alone.
func Sanitize(html string) string {
	clean := policy.Sanitize(html)
	return imgSrcPattern.ReplaceAllStringFunc(clean, func(match string) string {
		sub := imgSrcPattern.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		attrs, src := sub[1], sub[2]
		if strings.HasPrefix(src, "cid:") || strings.HasPrefix(src, "data:") {
			return match
		}
		return `<img` + attrs + ` src="` + placeholderSVG + `" data-spk-original-src="` + src + `"`
	})
}

// UnblockRemote restores the original src= for every <img> that has data-spk-original-src.
// Used after the user clicks "Show remote content".
func UnblockRemote(html string) string {
	return imgFullPattern.ReplaceAllStringFunc(html, func(match string) string {
		sub := imgFullPattern.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		attrs := sub[1]
		m := dataAttrPat.FindStringSubmatch(attrs)
		if len(m) != 2 {
			return match
		}
		newAttrs := dataAttrPat.ReplaceAllString(attrs, "")
		newAttrs = srcAttrPat.ReplaceAllString(newAttrs, ` src="`+m[1]+`"`)
		return `<img` + newAttrs + `>`
	})
}

// ExtractBlockedURLs returns the set of remote URLs currently held in
// data-spk-original-src placeholders inside html. Used to record the URLs
// the user is unblocking so future messages with the same URLs auto-unblock.
func ExtractBlockedURLs(html string) []string {
	matches := dataAttrPat.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) != 2 {
			continue
		}
		u := m[1]
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

// UnblockApproved restores src= ONLY for <img> whose data-spk-original-src
// URL is in the approved set. Other placeholders are left intact (still
// blocked, with their data-spk-original-src marker preserved so the UI
// can still offer "Show remote content"). Approved-set lookups are O(1).
func UnblockApproved(html string, approved map[string]bool) string {
	if len(approved) == 0 {
		return html
	}
	return imgFullPattern.ReplaceAllStringFunc(html, func(match string) string {
		sub := imgFullPattern.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		attrs := sub[1]
		m := dataAttrPat.FindStringSubmatch(attrs)
		if len(m) != 2 {
			return match
		}
		if !approved[m[1]] {
			return match
		}
		newAttrs := dataAttrPat.ReplaceAllString(attrs, "")
		newAttrs = srcAttrPat.ReplaceAllString(newAttrs, ` src="`+m[1]+`"`)
		return `<img` + newAttrs + `>`
	})
}
