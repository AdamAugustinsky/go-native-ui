package img

import (
	"encoding/binary"
	"image"
	"image/draw"
)

// How a frame is taken off the canvas before the next one (GIF's disposal
// methods 2 and 3 and APNG's dispose ops 1 and 2; "none" is 0 in both).
const (
	disposeNone uint8 = iota
	disposeBackground
	disposePrevious
)

// compositor holds the canvas of an animation at its source size and puts
// frames on it the way browsers do: the canvas starts transparent; before a
// frame is drawn the previous frame is disposed of (its rectangle cleared to
// transparent for "background", or restored to what was there before it for
// "previous"); a frame replaces the canvas under it ("source") or blends over
// it. Pixels are premultiplied RGBA.
type compositor struct {
	canvas *image.RGBA
	snap   []byte // the rectangle a "previous" frame covers, as it was before the frame
	tmp    []byte // a frame's pixels, converted
	// The previous frame, to dispose of before the next one.
	prevDisp uint8
	prevRect image.Rectangle
	frame    int // frames drawn since reset
}

func newCompositor(w, h int) *compositor {
	return &compositor{canvas: image.NewRGBA(image.Rect(0, 0, w, h))}
}

// reset clears the canvas for a replay from the first frame.
func (c *compositor) reset() {
	clear(c.canvas.Pix)
	c.prevDisp, c.prevRect, c.frame = disposeNone, image.Rectangle{}, 0
}

// dispose takes the previous frame off the canvas.
func (c *compositor) dispose() {
	r := c.prevRect.Intersect(c.canvas.Rect)
	switch c.prevDisp {
	case disposeBackground:
		c.fill(r, nil)
	case disposePrevious:
		c.fill(r, c.snap)
	}
}

// fill sets rectangle r to the pixels in snap (r's rows, packed), or to
// transparent when snap is nil.
func (c *compositor) fill(r image.Rectangle, snap []byte) {
	if r.Empty() {
		return
	}
	n := r.Dx() * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := c.canvas.Pix[c.canvas.PixOffset(r.Min.X, y):][:n]
		if snap == nil {
			clear(row)
		} else {
			copy(row, snap[(y-r.Min.Y)*n:])
		}
	}
}

// draw puts frame m, whose top left corner is at pos of the canvas, on the
// canvas: it first disposes of the previous frame, then blends m over (over)
// or replaces the canvas under it, and remembers how to dispose of m
// (disp). The first frame cannot restore the previous canvas: that is
// "background" (a viewer has nothing to restore).
func (c *compositor) draw(m image.Image, pos image.Point, disp uint8, over bool) {
	b := m.Bounds()
	r := c.prepare(image.Rectangle{Min: pos, Max: pos.Add(b.Size())}, disp)
	if r.Empty() {
		return
	}
	n := r.Dx() * 4
	// The visible part of the frame as premultiplied RGBA, in rows of n bytes.
	sp := b.Min.Add(r.Min.Sub(pos)) // the source point of r.Min
	var src []byte
	stride := n
	if rgba, ok := m.(*image.RGBA); ok {
		src, stride = rgba.Pix[rgba.PixOffset(sp.X, sp.Y):], rgba.Stride
	} else {
		c.tmp = grow(c.tmp, n*r.Dy())
		tmp := &image.RGBA{Pix: c.tmp, Stride: n, Rect: r}
		draw.Draw(tmp, r, m, sp, draw.Src)
		src = c.tmp
	}
	for y := 0; y < r.Dy(); y++ {
		d := c.canvas.Pix[c.canvas.PixOffset(r.Min.X, r.Min.Y+y):][:n]
		s := src[y*stride:][:n]
		if !over {
			copy(d, s)
			continue
		}
		for x := 0; x < n; x += 4 {
			switch a := uint32(s[x+3]); a {
			case 0:
			case 255:
				copy(d[x:x+4], s[x:x+4])
			default: // d = s + d·(1 − a)
				t := 255 - a
				for k := 0; k < 4; k++ {
					d[x+k] = byte(uint32(s[x+k]) + (uint32(d[x+k])*t+127)/255)
				}
			}
		}
	}
}

// prepare disposes the previous frame and saves the area this one restores.
func (c *compositor) prepare(full image.Rectangle, disp uint8) image.Rectangle {
	c.dispose()
	r := full.Intersect(c.canvas.Rect)
	if c.frame == 0 && disp == disposePrevious {
		disp = disposeBackground
	}
	c.prevDisp, c.prevRect = disp, r
	c.frame++
	if r.Empty() {
		return r
	}
	n := r.Dx() * 4
	if disp == disposePrevious {
		c.snap = grow(c.snap, n*r.Dy())
		for y := r.Min.Y; y < r.Max.Y; y++ {
			copy(c.snap[(y-r.Min.Y)*n:], c.canvas.Pix[c.canvas.PixOffset(r.Min.X, y):][:n])
		}
	}
	return r
}

// drawGIF composites opaque palette entries directly, skipping transparency.
func (c *compositor) drawGIF(indices []byte, full image.Rectangle, pal *[256]uint32, disp uint8, interlaced bool) {
	r := c.prepare(full, disp)
	if r.Empty() {
		return
	}
	passes := 1
	starts, steps := [4]int{}, [4]int{1}
	if interlaced {
		passes = 4
		starts, steps = [4]int{0, 4, 2, 1}, [4]int{8, 8, 4, 2}
	}
	row := 0
	for pass := 0; pass < passes; pass++ {
		for y := full.Min.Y + starts[pass]; y < full.Max.Y; y += steps[pass] {
			src := indices[row*full.Dx():][:full.Dx()]
			row++
			if y < r.Min.Y || y >= r.Max.Y {
				continue
			}
			dst := c.canvas.Pix[c.canvas.PixOffset(r.Min.X, y):][:r.Dx()*4]
			for x, v := range src[r.Min.X-full.Min.X : r.Max.X-full.Min.X] {
				if p := pal[v]; p != 0 {
					binary.LittleEndian.PutUint32(dst[x*4:], p)
				}
			}
		}
	}
}

func grow(b []byte, n int) []byte {
	if cap(b) < n {
		return make([]byte, n)
	}
	return b[:n]
}

// scaleCanvas draws the canvas at tw×th into the padded buffer pix (bw
// pixels per row, pad pixels of border), area averaged, and fills the
// border.
func scaleCanvas(pix []byte, bw int, pad int32, tw, th int32, m *image.RGBA) {
	dst := pix[(int(pad)*bw+int(pad))*4:]
	if b := m.Rect; b.Dx() == int(tw) && b.Dy() == int(th) {
		for y := 0; y < int(th); y++ {
			copy(dst[y*bw*4:y*bw*4+int(tw)*4], m.Pix[y*m.Stride:])
		}
	} else {
		resample(dst, bw*4, int(tw), int(th), m, 1)
	}
	if pad > 0 {
		padEdges(pix, int(tw), int(th))
	}
}
