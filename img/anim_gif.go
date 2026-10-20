package img

import (
	"bytes"
	"errors"
	"image"
	"image/gif"
)

// gifAnim decodes the frames of a GIF one at a time, in order, with the
// disposal and blending of browsers. The block structure is indexed once
// (no pixel is decoded); each frame is then decoded alone, by the standard
// library's decoder, from a one-frame GIF built from its blocks, so that the
// frames of a long animation are never all in memory. It works at the
// image's logical screen size; scaling to the display size is the caller's.
type gifAnim struct {
	data   []byte
	w, h   int
	head   []byte // signature, logical screen descriptor and global colour table
	frames []gifFrame
	plays  int // times the animation plays; 0 plays forever
	comp   *compositor
	next   int    // the frame the compositor is ready to draw
	mini   []byte // scratch: a one-frame GIF
}

// gifFrame is one frame's blocks and the delay and disposal of its graphic
// control extension.
type gifFrame struct {
	gce        [8]byte // the graphic control extension, if hasGCE
	hasGCE     bool
	start, end int32 // the image descriptor through its last data block
	delayMs    int32
	disposal   uint8
}

var errGIF = errors.New("img: malformed GIF")

// openGIF indexes the GIF in data.
func openGIF(data []byte) (*gifAnim, error) {
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return nil, errGIF
	}
	g := &gifAnim{data: data, plays: 1}
	g.w, g.h = int(data[6])|int(data[7])<<8, int(data[8])|int(data[9])<<8
	i := 13
	if data[10]&0x80 != 0 {
		i += 3 << (data[10]&7 + 1)
	}
	if i > len(data) {
		return nil, errGIF
	}
	g.head = data[:i]
	var gce [8]byte
	var hasGCE bool
	// skip passes the data sub-blocks at i; false if they run off the end.
	skip := func() bool {
		for i < len(data) {
			n := int(data[i])
			i++
			if n == 0 {
				return true
			}
			i += n
		}
		return false
	}
	for i < len(data) {
		switch data[i] {
		case 0x21: // extension
			if i+2 > len(data) {
				return g.finish()
			}
			label := data[i+1]
			switch {
			case label == 0xF9 && i+8 <= len(data) && data[i+2] == 4:
				copy(gce[:], data[i:i+8])
				hasGCE = true
			case label == 0xFF && i+19 <= len(data) && data[i+2] == 11 &&
				string(data[i+3:i+14]) == "NETSCAPE2.0" && data[i+14] == 3 && data[i+15] == 1:
				if n := int(data[i+16]) | int(data[i+17])<<8; n == 0 {
					g.plays = 0
				} else {
					g.plays = n + 1
				}
			}
			i += 2
			if !skip() {
				return g.finish()
			}
		case 0x2C: // image
			if i+10 > len(data) {
				return g.finish()
			}
			start := i
			flags := data[i+9]
			i += 10
			if flags&0x80 != 0 {
				i += 3 << (flags&7 + 1)
			}
			i++ // the LZW minimum code size
			if i > len(data) || !skip() {
				return g.finish()
			}
			f := gifFrame{gce: gce, hasGCE: hasGCE, start: int32(start), end: int32(i)}
			if hasGCE {
				f.delayMs = (int32(gce[4]) | int32(gce[5])<<8) * 10
				if d := (gce[3] >> 2) & 7; d == 2 {
					f.disposal = disposeBackground
				} else if d == 3 {
					f.disposal = disposePrevious
				}
			}
			g.frames = append(g.frames, f)
			hasGCE = false // a graphic control extension applies to the next image only
			if len(g.frames) > maxAnimFrames {
				return nil, errGIF
			}
		case 0x3B:
			return g.finish()
		default:
			return g.finish()
		}
	}
	return g.finish()
}

func (g *gifAnim) finish() (*gifAnim, error) {
	if len(g.frames) == 0 || g.w <= 0 || g.h <= 0 {
		return nil, errGIF
	}
	return g, nil
}

func (g *gifAnim) size() (w, h int) { return g.w, g.h }
func (g *gifAnim) count() int       { return len(g.frames) }
func (g *gifAnim) playCount() int   { return g.plays }
func (g *gifAnim) delay(i int) int  { return int(g.frames[i].delayMs) }

// frame composites frame i and returns the canvas (valid until the next
// call). Frames are cheap in order; asking for another frame replays the
// animation from the first.
func (g *gifAnim) frame(i int) (*image.RGBA, error) {
	if i < 0 || i >= len(g.frames) {
		return nil, errGIF
	}
	if g.comp == nil {
		g.comp = newCompositor(g.w, g.h)
	}
	if i != g.next {
		g.comp.reset()
		g.next = 0
		for g.next < i { // replay up to the frame
			if err := g.draw(g.next); err != nil {
				return nil, err
			}
		}
	}
	if err := g.draw(i); err != nil {
		return nil, err
	}
	return g.comp.canvas, nil
}

// draw decodes frame i and puts it on the canvas.
func (g *gifAnim) draw(i int) error {
	f := &g.frames[i]
	g.mini = append(g.mini[:0], g.head...)
	if f.hasGCE {
		g.mini = append(g.mini, f.gce[:]...)
	}
	g.mini = append(g.mini, g.data[f.start:f.end]...)
	g.mini = append(g.mini, 0x3B)
	// The one-frame GIF's logical screen is the real one: the frame keeps
	// its offset. (image/gif.Decode returns the frame's own paletted image.)
	m, err := gif.Decode(bytes.NewReader(g.mini))
	if err != nil {
		return err
	}
	g.comp.draw(m, m.Bounds().Min, f.disposal, true)
	g.next = i + 1
	return nil
}
