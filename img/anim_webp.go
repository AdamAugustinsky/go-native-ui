//go:build !darwin

package img

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"

	"golang.org/x/image/webp"
)

// webpAnim decodes the frames of an animated WebP one at a time, in order,
// with the disposal and blending of the WebP container specification. Like
// apngAnim it indexes the chunks once and decodes each frame alone, by
// x/image/webp, from a one-frame WebP built from its ANMF chunk's data.
// The canvas starts transparent and a disposed frame's rectangle is cleared
// to transparent: the ANIM chunk's background colour is a hint that
// browsers ignore too.
type webpAnim struct {
	data   []byte
	w, h   int
	frames []webpFrame
	plays  int // times the animation plays; 0 plays forever
	comp   *compositor
	next   int
	buf    []byte // scratch: a one-frame WebP
}

// webpFrame is an ANMF chunk: the frame's place on the canvas, its timing
// and operations, and its data (the ALPH, VP8 or VP8L chunks).
type webpFrame struct {
	rect    image.Rectangle
	delayMs int32
	dispose bool // dispose to the background (transparent) after showing
	over    bool // alpha-blend over the canvas; else replace it
	alpha   bool // the data has an ALPH chunk: the still needs a VP8X
	data    span
}

var errWebP = errors.New("img: invalid WebP animation")

// webpChunks calls f for each chunk of the RIFF WebP container in data, with
// its type and its payload, until f returns false; it reports whether the
// container is well formed up to where it stopped.
func webpChunks(data []byte, f func(typ string, off, n int) bool) bool {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	end := min(int64(binary.LittleEndian.Uint32(data[4:]))+8, int64(len(data)))
	for i := int64(12); i+8 <= end; {
		n := int64(binary.LittleEndian.Uint32(data[i+4:]))
		if n > end-i-8 {
			return false // runs past the end
		}
		if !f(string(data[i:i+4]), int(i+8), int(n)) {
			return true
		}
		i += 8 + n + n&1
	}
	return true
}

// webpAnimated reports whether the WebP in data is an animation: its VP8X
// chunk has the animation flag.
func webpAnimated(data []byte) bool {
	anim := false
	webpChunks(data, func(typ string, off, n int) bool {
		if typ == "VP8X" && n >= 10 {
			anim = data[off]&2 != 0
		}
		return false // VP8X is the first chunk, when there is one
	})
	return anim
}

// openWebP indexes the animated WebP in data; it fails for a still WebP
// (errNotAnimated) and for one that breaks the specification: a frame outside
// the canvas or with no image data, a canvas over the limit, chunks that run
// past the end.
func openWebP(data []byte) (*webpAnim, error) {
	a := &webpAnim{data: data, plays: 0}
	animated, sawANIM := false, false
	var err error
	ok := webpChunks(data, func(typ string, off, n int) bool {
		body := data[off : off+n]
		switch typ {
		case "VP8X":
			if n < 10 {
				err = errWebP
				return false
			}
			animated = body[0]&2 != 0
			w, h := int64(u24(body[4:]))+1, int64(u24(body[7:]))+1
			if !fitsCanvas(w, h) {
				err = errAnimLarge
				return false
			}
			a.w, a.h = int(w), int(h)
		case "ANIM":
			if n < 6 {
				err = errWebP
				return false
			}
			sawANIM = true
			a.plays = int(binary.LittleEndian.Uint16(body[4:]))
		case "ANMF":
			if !animated || a.w == 0 || n < 16+8 {
				err = errWebP
				return false
			}
			x, y := 2*int(u24(body)), 2*int(u24(body[3:]))
			w, h := int(u24(body[6:]))+1, int(u24(body[9:]))+1
			if x+w > a.w || y+h > a.h {
				err = errWebP
				return false
			}
			f := webpFrame{
				rect:    image.Rect(x, y, x+w, y+h),
				delayMs: int32(u24(body[12:])),
				dispose: body[15]&1 != 0,
				over:    body[15]&2 == 0,
				data:    span{int32(off + 16), int32(n - 16)},
			}
			hasImage := false
			webpSub(body[16:], func(typ string) {
				switch typ {
				case "ALPH":
					f.alpha = true
				case "VP8 ", "VP8L":
					hasImage = true
				}
			})
			if !hasImage {
				err = errWebP
				return false
			}
			if len(a.frames) >= maxAnimFrames {
				err = errAnimLarge
				return false
			}
			a.frames = append(a.frames, f)
		}
		return true
	})
	switch {
	case err != nil:
		return nil, err
	case !ok:
		return nil, errWebP
	case !animated:
		return nil, errNotAnimated
	case !sawANIM || len(a.frames) == 0:
		return nil, errWebP
	}
	return a, nil
}

// webpSub calls f with the type of each chunk in a frame's data.
func webpSub(b []byte, f func(typ string)) {
	for i := 0; i+8 <= len(b); {
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		if n < 0 || n > len(b)-i-8 {
			return
		}
		f(string(b[i : i+4]))
		i += 8 + n + n&1
	}
}

// u24 is a 24-bit little-endian number.
func u24(b []byte) uint32 { return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 }

func (a *webpAnim) size() (w, h int) { return a.w, a.h }

// retained: see gifAnim.
func (a *webpAnim) retained() int64 { return int64(len(a.data)) + 3*4*int64(a.w)*int64(a.h) }
func (a *webpAnim) count() int      { return len(a.frames) }

// trim: see gifAnim.
func (a *webpAnim) trim() int64 {
	a.comp, a.buf, a.next = nil, nil, 0
	return int64(len(a.data))
}

func (a *webpAnim) playCount() int  { return a.plays }
func (a *webpAnim) delay(i int) int { return int(a.frames[i].delayMs) }

// frame composites frame i; see gifAnim.frame.
func (a *webpAnim) frame(i int) (*image.RGBA, error) {
	if i < 0 || i >= len(a.frames) {
		return nil, errWebP
	}
	if a.comp == nil {
		a.comp = newCompositor(a.w, a.h)
	}
	if i != a.next {
		a.comp.reset()
		a.next = 0
		for a.next < i {
			if err := a.draw(a.next); err != nil {
				return nil, err
			}
		}
	}
	if err := a.draw(i); err != nil {
		return nil, err
	}
	return a.comp.canvas, nil
}

// draw decodes frame i as a WebP of its own (a VP8X chunk when it has an
// alpha channel, then its data chunks) and puts it on the canvas.
func (a *webpAnim) draw(i int) error {
	f := &a.frames[i]
	frame := a.data[f.data.off : f.data.off+f.data.n]
	a.buf = append(a.buf[:0], "RIFF\x00\x00\x00\x00WEBP"...)
	if f.alpha {
		var x [18]byte
		copy(x[:], "VP8X")
		x[4] = 10
		x[8] = 0x10 // alpha
		w, h := f.rect.Dx()-1, f.rect.Dy()-1
		x[12], x[13], x[14] = byte(w), byte(w>>8), byte(w>>16)
		x[15], x[16], x[17] = byte(h), byte(h>>8), byte(h>>16)
		a.buf = append(a.buf, x[:]...)
	}
	a.buf = append(a.buf, frame...)
	binary.LittleEndian.PutUint32(a.buf[4:], uint32(len(a.buf)-8))
	m, err := webp.Decode(bytes.NewReader(a.buf))
	if err != nil {
		return err
	}
	if m.Bounds().Dx() != f.rect.Dx() || m.Bounds().Dy() != f.rect.Dy() {
		return errWebP
	}
	disp := disposeNone
	if f.dispose {
		disp = disposeBackground
	}
	a.comp.draw(m, f.rect.Min, disp, f.over)
	a.next = i + 1
	return nil
}
