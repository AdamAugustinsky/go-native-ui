package ui

import "gonativeui/gpu"

// ImageFit says how DrawImage places a box's image in its padded rectangle.
type ImageFit uint8

const (
	// FitNatural draws the image at its own size (its texel rectangle),
	// centred, with square corners: icons and symbols. The default.
	FitNatural ImageFit = iota
	// FitFill scales the image to cover the rectangle, keeping its aspect,
	// and crops what overflows (avatars, grid thumbnails).
	FitFill
	// FitContain scales the image to fit inside the rectangle, keeping its
	// aspect; the rest of the rectangle stays empty (media previews).
	FitContain
	// FitCenter draws the image at its own size, centred, cropped to the
	// rectangle.
	FitCenter
)

// emitFitted draws b's image placed by its Fit, rounded by the box's radii,
// premultiplied colour drawn with the alpha of the text colour.
func (c *Context) emitFitted(l *gpu.DrawList, b *box, clip [4]float32) {
	im := &b.img
	iw, ih := im.UV[2]-im.UV[0], im.UV[3]-im.UV[1]
	r := b.rect
	x0, y0, x1, y1 := r[0]+b.pad[0], r[1]+b.pad[1], r[2]-b.pad[2], r[3]-b.pad[3]
	bw, bh := x1-x0, y1-y0
	if iw <= 0 || ih <= 0 || bw <= 0 || bh <= 0 {
		return
	}
	a := b.col(b.textColor) & 255
	if a == 0 {
		return
	}
	col := 0xFFFFFF00 | a
	uv := im.UV
	rect := [4]float32{x0, y0, x1, y1}
	switch im.Fit {
	case FitFill: // the box, showing the middle of the image
		s := max(bw/iw, bh/ih)
		cw, ch := bw/s, bh/s
		uv[0] += (iw - cw) / 2
		uv[1] += (ih - ch) / 2
		uv[2], uv[3] = uv[0]+cw, uv[1]+ch
	case FitContain: // the whole image, centred, snapped to pixels
		s := min(bw/iw, bh/ih)
		dw, dh := iw*s, ih*s
		rect[0], rect[1] = snap(x0+(bw-dw)/2), snap(y0+(bh-dh)/2)
		rect[2], rect[3] = rect[0]+snap(dw), rect[1]+snap(dh)
	case FitCenter: // texel for pixel, what overflows cropped
		ix, iy := snap(x0+(bw-iw)/2), snap(y0+(bh-ih)/2)
		rect = [4]float32{max(ix, x0), max(iy, y0), min(ix+iw, x1), min(iy+ih, y1)}
		uv = [4]float32{uv[0] + rect[0] - ix, uv[1] + rect[1] - iy, uv[0] + rect[2] - ix, uv[1] + rect[3] - iy}
	}
	if empty(intersect(rect, clip)) {
		return
	}
	l.Add(gpu.Quad{
		Rect:  rect,
		Clip:  clip,
		UV:    uv,
		Color: [4]uint32{col, col, col, col},
		Radii: b.radii,
		Mode:  gpu.ModeRGBA,
		Tex:   im.Tex,
	})
}
