// Package animgen makes animated GIFs and APNGs for tests and demos: images
// of our own, so that no third-party files are checked in. Both encoders
// take the same Frame list, with the disposal and blend modes the formats
// share.
package animgen

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
)

// Dispose says what happens to a frame's rectangle once its time is up.
type Dispose uint8

const (
	// DisposeNone leaves the frame on the canvas.
	DisposeNone Dispose = iota
	// DisposeBackground clears the frame's rectangle to transparent.
	DisposeBackground
	// DisposePrevious restores the canvas to what it was before the frame.
	DisposePrevious
)

// Frame is one frame of an animation: the pixels of a rectangle of the
// canvas (Image.Bounds is that rectangle), how long it shows, and how it is
// put on the canvas and taken off again.
type Frame struct {
	Image   *image.NRGBA
	DelayMs int
	Dispose Dispose
	// Over blends the frame over the canvas (APNG's blend op OVER). A GIF
	// frame always blends: its transparent pixels leave the canvas alone.
	Over bool
}

// Solid is a w×h image of one colour.
func Solid(w, h int, c color.NRGBA) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(m, m.Rect, &image.Uniform{c}, image.Point{}, draw.Src)
	return m
}

// At is m moved so that its top left corner is at (x, y) of the canvas.
func At(m *image.NRGBA, x, y int) *image.NRGBA {
	return &image.NRGBA{Pix: m.Pix, Stride: m.Stride, Rect: m.Rect.Add(image.Pt(x, y).Sub(m.Rect.Min))}
}

// GIF encodes frames on a w×h canvas. loopCount is the NETSCAPE loop count
// as image/gif has it: 0 loops forever, -1 plays once (no extension), n
// repeats n more times. Frame images use at most 255 colours plus
// transparency: they are quantized to the colours they hold (the encoder
// here is for flat test art).
func GIF(w, h int, frames []Frame, loopCount int) []byte {
	g := &gif.GIF{LoopCount: loopCount, Config: image.Config{Width: w, Height: h}}
	for _, f := range frames {
		p := image.NewPaletted(f.Image.Rect, nil)
		index := map[color.NRGBA]uint8{}
		var pal color.Palette
		for y := f.Image.Rect.Min.Y; y < f.Image.Rect.Max.Y; y++ {
			for x := f.Image.Rect.Min.X; x < f.Image.Rect.Max.X; x++ {
				c := f.Image.NRGBAAt(x, y)
				if c.A != 255 {
					c = color.NRGBA{}
				}
				i, ok := index[c]
				if !ok {
					if len(pal) == 256 {
						panic("animgen: more than 256 colours in a GIF frame")
					}
					i = uint8(len(pal))
					index[c] = i
					pal = append(pal, c)
				}
				p.SetColorIndex(x, y, i)
			}
		}
		p.Palette = pal
		g.Image = append(g.Image, p)
		g.Delay = append(g.Delay, (f.DelayMs+5)/10)
		switch f.Dispose {
		case DisposeBackground:
			g.Disposal = append(g.Disposal, gif.DisposalBackground)
		case DisposePrevious:
			g.Disposal = append(g.Disposal, gif.DisposalPrevious)
		default:
			g.Disposal = append(g.Disposal, gif.DisposalNone)
		}
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// APNG encodes frames on a w×h canvas. plays is the number of times the
// animation plays; 0 plays forever. The first frame is the default image, so
// a viewer without APNG support shows it.
func APNG(w, h int, frames []Frame, plays int) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(typ string, data []byte) {
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
		copy(hdr[4:], typ)
		b.Write(hdr[:])
		b.Write(data)
		crc := crc32.NewIEEE()
		crc.Write(hdr[4:])
		crc.Write(data)
		binary.Write(&b, binary.BigEndian, crc.Sum32())
	}
	u32 := func(vs ...uint32) []byte {
		out := make([]byte, 0, 4*len(vs))
		for _, v := range vs {
			out = binary.BigEndian.AppendUint32(out, v)
		}
		return out
	}
	chunk("IHDR", append(u32(uint32(w), uint32(h)), 8, 6, 0, 0, 0)) // 8-bit RGBA
	chunk("acTL", u32(uint32(len(frames)), uint32(plays)))
	seq := uint32(0)
	for i, f := range frames {
		r := f.Image.Rect
		fc := u32(seq, uint32(r.Dx()), uint32(r.Dy()), uint32(r.Min.X), uint32(r.Min.Y))
		fc = binary.BigEndian.AppendUint16(fc, uint16(f.DelayMs))
		fc = binary.BigEndian.AppendUint16(fc, 1000)
		blend := byte(0)
		if f.Over {
			blend = 1
		}
		fc = append(fc, byte(f.Dispose), blend)
		chunk("fcTL", fc)
		seq++
		data := compress(f.Image)
		if i == 0 {
			chunk("IDAT", data)
		} else {
			chunk("fdAT", append(u32(seq), data...))
			seq++
		}
	}
	chunk("IEND", nil)
	return b.Bytes()
}

// compress is m's rows, unfiltered, as a zlib stream.
func compress(m *image.NRGBA) []byte {
	var raw bytes.Buffer
	w, h := m.Rect.Dx(), m.Rect.Dy()
	for y := 0; y < h; y++ {
		raw.WriteByte(0)
		raw.Write(m.Pix[y*m.Stride : y*m.Stride+w*4])
	}
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	zw.Write(raw.Bytes())
	zw.Close()
	return out.Bytes()
}

// Ball is a w×h animation of n frames: a disc travelling around the
// canvas on a transparent background, each frame drawn over the last with
// DisposeBackground (the disc's own rectangle is cleared). Used by the
// gallery.
func Ball(w, h, n, delayMs int, col color.NRGBA) []Frame {
	frames := make([]Frame, n)
	r := min(w, h) / 6
	for i := range frames {
		a := 2 * math.Pi * float64(i) / float64(n)
		cx := w/2 + int(float64(w/2-r-2)*math.Cos(a))
		cy := h/2 + int(float64(h/2-r-2)*math.Sin(a))
		box := image.Rect(cx-r, cy-r, cx+r, cy+r)
		m := image.NewNRGBA(box)
		for y := box.Min.Y; y < box.Max.Y; y++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				dx, dy := x-cx, y-cy
				if dx*dx+dy*dy <= r*r {
					m.SetNRGBA(x, y, col)
				}
			}
		}
		frames[i] = Frame{Image: m, DelayMs: delayMs, Dispose: DisposeBackground, Over: true}
	}
	return frames
}

// Sheet is n full-canvas frames that sweep a hue ramp: opaque, one colour
// per frame, shown for delayMs each. Sheet(…)[i] is the same colour in a
// GIF and an APNG.
func Sheet(w, h, n, delayMs int) []Frame {
	frames := make([]Frame, n)
	for i := range frames {
		t := float64(i) / float64(n)
		frames[i] = Frame{Image: Solid(w, h, hue(t)), DelayMs: delayMs}
	}
	return frames
}

func hue(t float64) color.NRGBA {
	f := func(o float64) uint8 {
		v := 0.5 + 0.5*math.Cos(2*math.Pi*(t+o))
		return uint8(v * 255)
	}
	return color.NRGBA{f(0), f(1.0 / 3), f(2.0 / 3), 255}
}
