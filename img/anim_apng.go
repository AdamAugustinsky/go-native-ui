package img

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
)

// apngAnim decodes the frames of an animated PNG one at a time, in order,
// with the disposal and blending of the APNG specification. Like gifAnim it
// indexes the chunks once and decodes each frame alone, by the standard
// library's PNG decoder, from a one-frame PNG built from its data chunks.
type apngAnim struct {
	data   []byte
	w, h   int
	ihdr   []byte // the IHDR data (the frame's size replaces its first 8 bytes)
	shared [][]byte
	frames []apngFrame
	plays  int // times the animation plays; 0 plays forever
	comp   *compositor
	next   int
	buf    []byte // scratch: a one-frame PNG
	idat   []byte // scratch: a frame's compressed data
}

// apngFrame is a frame's control chunk and the places of its data.
type apngFrame struct {
	rect     image.Rectangle
	delayMs  int32
	disposal uint8
	over     bool
	data     []span
}

// span is a stretch of the file: the payload of one IDAT or fdAT chunk,
// without fdAT's sequence number.
type span struct{ off, n int32 }

var errAPNG = errors.New("img: malformed APNG")

const pngSig = "\x89PNG\r\n\x1a\n"

// openAPNG indexes the animated PNG in data; it fails for a plain PNG
// (errNotAnimated).
func openAPNG(data []byte) (*apngAnim, error) {
	if len(data) < 8+25 || string(data[:8]) != pngSig {
		return nil, errAPNG
	}
	a := &apngAnim{data: data, plays: 1}
	var cur *apngFrame
	animated, sawIDAT := false, false
	i := 8
	for i+12 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[i:]))
		typ := string(data[i+4 : i+8])
		if n < 0 || i+12+n > len(data) {
			break
		}
		body := data[i+8 : i+8+n]
		switch typ {
		case "IHDR":
			if n != 13 {
				return nil, errAPNG
			}
			a.w, a.h = int(binary.BigEndian.Uint32(body)), int(binary.BigEndian.Uint32(body[4:]))
			a.ihdr = body
		case "acTL":
			if n != 8 || sawIDAT {
				return nil, errAPNG
			}
			animated = true
			a.plays = int(binary.BigEndian.Uint32(body[4:]))
			if a.plays > 1<<20 { // absurd; the field is 32 bits
				a.plays = 0
			}
		case "fcTL":
			if n != 26 || !animated {
				return nil, errAPNG
			}
			f := apngFrame{}
			w, h := int(binary.BigEndian.Uint32(body[4:])), int(binary.BigEndian.Uint32(body[8:]))
			x, y := int(binary.BigEndian.Uint32(body[12:])), int(binary.BigEndian.Uint32(body[16:]))
			num, den := int32(binary.BigEndian.Uint16(body[20:])), int32(binary.BigEndian.Uint16(body[22:]))
			if den == 0 {
				den = 100
			}
			f.delayMs = int32((int64(num)*1000 + int64(den)/2) / int64(den))
			f.rect = image.Rect(x, y, x+w, y+h)
			if w <= 0 || h <= 0 || x < 0 || y < 0 || x+w > a.w || y+h > a.h {
				return nil, errAPNG
			}
			switch body[24] {
			case 1:
				f.disposal = disposeBackground
			case 2:
				f.disposal = disposePrevious
			}
			f.over = body[25] == 1
			a.frames = append(a.frames, f)
			cur = &a.frames[len(a.frames)-1]
			if len(a.frames) > maxAnimFrames {
				return nil, errAPNG
			}
		case "IDAT":
			s := span{int32(i + 8), int32(n)}
			sawIDAT = true
			if cur != nil && len(a.frames) == 1 { // the first frame's control chunk came before the data: the default image is frame 0
				cur.data = append(cur.data, s)
			}
		case "fdAT":
			if n < 4 || cur == nil {
				return nil, errAPNG
			}
			cur.data = append(cur.data, span{int32(i + 12), int32(n - 4)})
		case "IEND":
			i = len(data)
			continue
		default:
			if !sawIDAT && typ != "acTL" { // ancillary chunks (palette, transparency, colour) before the image data describe every frame
				a.shared = append(a.shared, data[i:i+12+n])
			}
		}
		i += 12 + n
	}
	if !animated || len(a.frames) == 0 || a.w <= 0 || a.h <= 0 {
		return nil, errNotAnimated
	}
	for k := range a.frames {
		if len(a.frames[k].data) == 0 {
			return nil, errAPNG
		}
	}
	return a, nil
}

var errNotAnimated = errors.New("img: not an animation")

func (a *apngAnim) size() (w, h int) { return a.w, a.h }
func (a *apngAnim) count() int       { return len(a.frames) }
func (a *apngAnim) playCount() int   { return a.plays }
func (a *apngAnim) delay(i int) int  { return int(a.frames[i].delayMs) }

// frame composites frame i; see gifAnim.frame.
func (a *apngAnim) frame(i int) (*image.RGBA, error) {
	if i < 0 || i >= len(a.frames) {
		return nil, errAPNG
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

func (a *apngAnim) draw(i int) error {
	f := &a.frames[i]
	// The frame as a PNG of its own size: the canvas' header with the
	// frame's width and height, the shared chunks, the frame's data as one
	// IDAT, the end.
	a.idat = a.idat[:0]
	for _, s := range f.data {
		a.idat = append(a.idat, a.data[s.off:s.off+s.n]...)
	}
	a.buf = append(a.buf[:0], pngSig...)
	var ihdr [13]byte
	copy(ihdr[:], a.ihdr)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(f.rect.Dx()))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(f.rect.Dy()))
	a.chunk("IHDR", ihdr[:])
	for _, c := range a.shared {
		a.buf = append(a.buf, c...)
	}
	a.chunk("IDAT", a.idat)
	a.chunk("IEND", nil)
	m, err := png.Decode(bytes.NewReader(a.buf))
	if err != nil {
		return err
	}
	if m.Bounds().Dx() != f.rect.Dx() || m.Bounds().Dy() != f.rect.Dy() {
		return errAPNG
	}
	a.comp.draw(m, f.rect.Min, f.disposal, f.over)
	a.next = i + 1
	return nil
}

// chunk appends a PNG chunk to the scratch PNG.
func (a *apngAnim) chunk(typ string, body []byte) {
	a.buf = binary.BigEndian.AppendUint32(a.buf, uint32(len(body)))
	start := len(a.buf)
	a.buf = append(a.buf, typ...)
	a.buf = append(a.buf, body...)
	a.buf = binary.BigEndian.AppendUint32(a.buf, crc32.ChecksumIEEE(a.buf[start:]))
}
