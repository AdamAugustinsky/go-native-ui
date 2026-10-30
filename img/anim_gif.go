package img

import (
	"compress/lzw"
	"errors"
	"image"
	"io"
	"unsafe"
)

// gifAnim decodes the frames of a GIF one at a time, in order, with the
// disposal and blending of browsers. The block structure is indexed once
// (no pixel is decoded); a reusable LZW decoder reads each frame's
// sub-blocks into one index buffer. Frames are never all decoded in memory.
// It works at the image's logical screen size; scaling to the display size is the caller's.
type gifAnim struct {
	data     []byte
	w, h     int
	head     []byte // signature, logical screen descriptor and global colour table
	frames   []gifFrame
	plays    int // times the animation plays; 0 plays forever
	comp     *compositor
	next     int // the frame the compositor is ready to draw
	maxFrame int
	indices  []byte
	lzw      *lzw.Reader
	blocks   gifBlocks
	palette  [256]uint32
	extra    [1]byte
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
	if g.w == 0 || g.h == 0 {
		return nil, errGIF
	}
	if !fitsCanvas(int64(g.w), int64(g.h)) {
		return nil, errAnimLarge
	}
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
			// A frame's index buffer must fit the canvas limit too.
			fw, fh := int64(data[i+5])|int64(data[i+6])<<8, int64(data[i+7])|int64(data[i+8])<<8
			if fw == 0 || fh == 0 {
				return nil, errGIF
			}
			if !fitsCanvas(fw, fh) {
				return nil, errAnimLarge
			}
			i += 10
			if flags&0x80 != 0 {
				i += 3 << (flags&7 + 1)
			}
			i++ // the LZW minimum code size
			if i > len(data) || !skip() {
				return g.finish()
			}
			g.maxFrame = max(g.maxFrame, int(fw*fh))
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

// retained bounds the file, canvas, disposal snapshot, indices and LZW tables.
func (g *gifAnim) retained() int64 {
	return int64(len(g.data)) + 8*int64(g.w)*int64(g.h) + int64(g.maxFrame) + int64(unsafe.Sizeof(lzw.Reader{}))
}
func (g *gifAnim) count() int { return len(g.frames) }

// trim drops the canvases; frame makes them again, from the first frame.
func (g *gifAnim) trim() int64 {
	g.comp, g.indices, g.lzw, g.next = nil, nil, nil, 0
	g.blocks = gifBlocks{}
	return int64(len(g.data))
}

func (g *gifAnim) playCount() int  { return g.plays }
func (g *gifAnim) delay(i int) int { return int(g.frames[i].delayMs) }

// frame composites frame i and returns the canvas (valid until the next
// call). Frames are cheap in order; asking for another frame replays the
// animation from the first.
func (g *gifAnim) frame(i int) (*image.RGBA, error) {
	if i < 0 || i >= len(g.frames) {
		return nil, errGIF
	}
	if g.comp == nil {
		g.comp = newCompositor(g.w, g.h)
		g.indices = make([]byte, g.maxFrame)
		g.lzw = new(lzw.Reader)
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
	b := g.data[f.start:f.end]
	x, y := int(b[1])|int(b[2])<<8, int(b[3])|int(b[4])<<8
	fw, fh := int(b[5])|int(b[6])<<8, int(b[7])|int(b[8])<<8
	if x+fw > g.w || y+fh > g.h {
		return errGIF
	}
	pal := g.head[13:]
	data := b[10:]
	if b[9]&0x80 != 0 {
		n := 3 << (b[9]&7 + 1)
		pal, data = data[:n], data[n:]
	}
	if len(pal) == 0 || len(data) == 0 || data[0] < 2 || data[0] > 8 {
		return errGIF
	}
	g.blocks = gifBlocks{data: data[1:]}
	g.lzw.Reset(&g.blocks, lzw.LSB, int(data[0]))
	indices := g.indices[:fw*fh]
	if _, err := io.ReadFull(g.lzw, indices); err != nil {
		return err
	}
	if n, err := g.lzw.Read(g.extra[:]); n != 0 || err != io.EOF {
		return errGIF
	}
	trans := -1
	if f.hasGCE && f.gce[3]&1 != 0 {
		trans = int(f.gce[6])
	}
	npal := len(pal) / 3
	clear(g.palette[:])
	for i := 0; i < npal; i++ {
		if i != trans {
			g.palette[i] = uint32(pal[3*i]) | uint32(pal[3*i+1])<<8 | uint32(pal[3*i+2])<<16 | 0xff000000
		}
	}
	if npal < 256 {
		for _, v := range indices {
			if int(v) >= npal && int(v) != trans {
				return errGIF
			}
		}
	}
	g.comp.drawGIF(indices, image.Rect(x, y, x+fw, y+fh), &g.palette, f.disposal, b[9]&0x40 != 0)
	g.next = i + 1
	return nil
}

// gifBlocks presents GIF data sub-blocks as a byte stream to LZW.
type gifBlocks struct {
	data      []byte
	pos, left int
}

func (b *gifBlocks) ReadByte() (byte, error) {
	if b.left == 0 {
		if b.pos >= len(b.data) || b.data[b.pos] == 0 {
			return 0, io.EOF
		}
		b.left = int(b.data[b.pos])
		b.pos++
	}
	if b.pos >= len(b.data) {
		return 0, io.ErrUnexpectedEOF
	}
	v := b.data[b.pos]
	b.pos++
	b.left--
	return v, nil
}

func (b *gifBlocks) Read(p []byte) (int, error) {
	for i := range p {
		v, err := b.ReadByte()
		if err != nil {
			return i, err
		}
		p[i] = v
	}
	return len(p), nil
}
