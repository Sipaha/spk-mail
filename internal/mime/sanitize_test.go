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
