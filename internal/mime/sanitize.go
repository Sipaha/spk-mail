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
	p.AllowElements("style") // styled emails are common
	p.AllowAttrs("style").Globally()
	p.AllowAttrs("class").Globally()
	p.AllowURLSchemes("http", "https", "mailto", "cid", "data")
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
