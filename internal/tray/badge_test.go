package tray

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func simpleBaseIcon(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestRenderBadge_DecodesAsPNG(t *testing.T) {
	base := simpleBaseIcon(64, 64)
	out, err := RenderBadge(base, 12)
	require.NoError(t, err)
	img, _, err := image.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	require.Equal(t, 64, img.Bounds().Dx())
}

func TestRenderBadge_ZeroReturnsBase(t *testing.T) {
	base := simpleBaseIcon(64, 64)
	out, _ := RenderBadge(base, 0)
	require.Equal(t, base, out)
}
