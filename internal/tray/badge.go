// Package tray composes desktop tray icons with overlays such as unread badges.
package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// RenderBadge overlays a red circular badge with count on top of base PNG.
// If count is 0, returns base unchanged.
func RenderBadge(base []byte, count int) ([]byte, error) {
	if count <= 0 {
		return base, nil
	}
	src, err := png.Decode(bytes.NewReader(base))
	if err != nil {
		return nil, err
	}
	bounds := src.Bounds()
	out := image.NewNRGBA(bounds)
	draw.Draw(out, bounds, src, bounds.Min, draw.Src)

	r := bounds.Dx() / 4
	cx := bounds.Max.X - r - 1
	cy := bounds.Min.Y + r + 1
	red := color.NRGBA{220, 38, 38, 255}
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r {
				out.SetNRGBA(x, y, red)
			}
		}
	}
	label := strconv.Itoa(count)
	if count > 99 {
		label = "99+"
	}
	face := basicfont.Face7x13
	w := font.MeasureString(face, label).Round()
	d := &font.Drawer{
		Dst:  out,
		Src:  image.NewUniform(color.White),
		Face: face,
		Dot:  fixed.P(cx-w/2, cy+face.Metrics().Ascent.Round()/2-1),
	}
	d.DrawString(label)

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
