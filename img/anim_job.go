package img

import (
	"bytes"
	"errors"
	"image"
	"io"
	"os"

	"gonativeui/task"
)

// taskResult is a finished decode as the pools return it.
type taskResult = task.Result[result]

// animDecoder produces the frames of one animation. A worker uses it for one
// call at a time (the cache has at most one job out per animation), but
// successive calls may come from different workers.
type animDecoder interface {
	// size is the canvas in pixels; count the frames in one play; playCount
	// the times the file plays (0 for ever); delay a frame's raw display
	// time in ms.
	size() (w, h int)
	count() int
	playCount() int
	delay(i int) int
	// retained is the memory the decoder holds between frames, in bytes
	// (an estimate: the encoded file and its canvases), which the cache
	// counts against its CPU budget.
	retained() int64
	// render draws frame i, composited on the canvas, scaled to tw×th, into
	// pix: premultiplied RGBA, bw pixels per row, with pad pixels of border
	// around the picture (the border is filled with the edge pixels). Frames
	// are cheap in order and costly out of order.
	render(w *worker, i int, tw, th, pad int32, pix []byte, bw int) error
	// close frees the decoder's resources (C objects on macOS).
	close(fr *cframe)
}

// frameSource is a pure Go animation decoder (GIF, APNG).
type frameSource interface {
	size() (w, h int)
	count() int
	playCount() int
	delay(i int) int
	retained() int64
	frame(i int) (*image.RGBA, error)
}

// stdAnim is an animDecoder over a frameSource: it composites at the
// canvas size and scales each frame to the display size.
type stdAnim struct{ frameSource }

func (s stdAnim) render(_ *worker, i int, tw, th, pad int32, pix []byte, bw int) error {
	m, err := s.frame(i)
	if err != nil {
		return err
	}
	scaleCanvas(pix, bw, pad, tw, th, m)
	return nil
}

func (stdAnim) close(*cframe) {}

// errAnimLarge: an animation's canvas (or a frame) is over the limit.
var errAnimLarge = errors.New("img: animation too large")

// fitsCanvas reports whether a w×h canvas is within animMaxCanvas pixels. It
// divides, so that no header, however hostile, overflows the product (an APNG
// header says 0xffffffff by 0xffffffff).
func fitsCanvas(w, h int64) bool { return w > 0 && h > 0 && w <= animMaxCanvas/h }

// Limits of animations.
const (
	// maxAnimFrames is the most frames an animation may have.
	maxAnimFrames = 1 << 16

	// animMaxCanvas is the most pixels of an animation's canvas: a frame is
	// composited at that size with a second canvas beside it to restore
	// (and scaled from a third), so that the 100 megapixels of a still
	// image (maxPixels) bound the frames decoded at once.
	animMaxCanvas = maxPixels / 4
	// animMaxBytes bounds the pixels an animation decodes over one play:
	// canvas × frames × 4 bytes.
	animMaxBytes = 2 << 30
	// animMaxFile is the largest encoded animation held in memory.
	animMaxFile = 256 << 20
)

// openAnimHook, when set (by tests), builds the decoder for the bytes it
// recognises, in place of the system's: nil, nil passes.
var errDecodeAnim = errors.New("img: cannot decode the animation")

var openAnimHook func(data []byte) (animDecoder, error)

// animCandidate reports whether a file of size bytes, read through r, is of a
// format that can be an animation here: a GIF; a PNG with an animation
// control chunk; where the system decodes them, a WebP with the animation
// flag or an ANIM chunk, or a HEIF. It walks the container's chunk headers,
// wherever in the file they are (a large colour profile may come first), and
// reads no pixels.
func animCandidate(r io.ReaderAt, size int64) bool {
	if openAnimHook != nil {
		return true
	}
	var head [12]byte
	n, _ := r.ReadAt(head[:], 0)
	switch sniff(head[:n]) {
	case "gif":
		return true
	case "png":
		return chunkWalk(r, size, 8, false, func(typ string, _ int64, _ int64) (found, stop bool) {
			return typ == "acTL", typ == "IDAT" || typ == "IEND"
		})
	case "webp":
		return osAnimWebP && chunkWalk(r, size, 12, true, func(typ string, off, n int64) (found, stop bool) {
			switch typ {
			case "ANIM":
				return true, true
			case "VP8X":
				var f [1]byte
				if _, err := r.ReadAt(f[:], off+8); err == nil {
					return f[0]&2 != 0, true
				}
				return false, true
			case "VP8 ", "VP8L", "ALPH":
				return false, true
			}
			return false, false
		})
	case "heif":
		return osAnimHEIF
	}
	return false
}

// chunkWalk visits the chunk headers of a PNG (8-byte signature, big-endian
// lengths, a 4-byte CRC after each chunk) or a RIFF file (riff: 12-byte
// start, little-endian lengths, even padding), from offset start, until visit
// stops it or the file ends. It reports whether visit found what it looked
// for.
func chunkWalk(r io.ReaderAt, size, start int64, riff bool, visit func(typ string, off, n int64) (found, stop bool)) bool {
	off := start
	for i := 0; i < 1<<20 && off+8 <= size; i++ {
		var h [8]byte
		if _, err := r.ReadAt(h[:], off); err != nil {
			return false
		}
		var n int64
		if riff {
			n = int64(uint32(h[4]) | uint32(h[5])<<8 | uint32(h[6])<<16 | uint32(h[7])<<24)
		} else {
			n = int64(uint32(h[0])<<24 | uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3]))
			h = [8]byte{h[4], h[5], h[6], h[7]}
		}
		typ, body := string(h[:4]), off+8
		if found, stop := visit(typ, off, n); found || stop {
			return found
		}
		off = body + n
		if riff {
			off += n & 1
		} else {
			off += 4
		}
	}
	return false
}

// openAnim builds the animation of an Animate job and decodes its first
// frame. handled is false when the image is not an animation (a still image,
// or a file of one frame): the caller decodes it as a still image.
func (w *worker) openAnim(c *Cache, j *job, data []byte) (r result, a *anim, handled bool) {
	fail := func(code uint8) (result, *anim, bool) { return result{buf: -1, err: code}, nil, true }
	var own []byte
	if j.src.kind == srcFile {
		f, err := os.Open(j.src.path)
		if err != nil {
			return fail(errRead)
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return fail(errRead)
		}
		if !animCandidate(f, st.Size()) {
			return result{}, nil, false
		}
		if st.Size() > animMaxFile {
			return fail(errTooLarge)
		}
		own = make([]byte, st.Size())
		if _, err := io.ReadFull(io.NewSectionReader(f, 0, st.Size()), own); err != nil {
			return fail(errRead)
		}
	} else {
		if !animCandidate(bytes.NewReader(data), int64(len(data))) {
			return result{}, nil, false
		}
		if len(data) > animMaxFile {
			return fail(errTooLarge)
		}
		own = append([]byte(nil), data...)
	}
	dec, err := w.openDecoder(own)
	if dec == nil {
		switch {
		case err == nil || errors.Is(err, errNotAnimated):
			return result{}, nil, false
		case errors.Is(err, errAnimLarge):
			return fail(errTooLarge)
		}
		return fail(errDecode)
	}
	n := dec.count()
	cw, ch := dec.size()
	if n <= 1 {
		dec.close(&w.fr)
		return result{}, nil, false
	}
	if !fitsCanvas(int64(cw), int64(ch)) || n > maxAnimFrames || int64(cw)*int64(ch)*4*int64(n) > animMaxBytes {
		dec.close(&w.fr)
		return fail(errTooLarge)
	}
	tw, th := scaled(int32(cw), int32(ch), fitScale(int32(cw), int32(ch), j.w, j.h, j.flags&Cover != 0))
	if tw > maxSide || th > maxSide {
		dec.close(&w.fr)
		return fail(errTooLarge)
	}
	pad := atlasPad(tw, th)
	bw, bh := int(tw+2*pad), int(th+2*pad)
	buf := c.bufs.get(bw*bh*4, &w.fr)
	if buf < 0 {
		dec.close(&w.fr)
		return fail(errTooLarge)
	}
	if err := dec.render(w, 0, tw, th, pad, c.bufs.bytes(buf, bw*bh*4), bw); err != nil {
		c.bufs.put(buf, &w.fr)
		dec.close(&w.fr)
		return fail(errDecode)
	}
	a = &anim{dec: dec, frames: int32(n), plays: int32(dec.playCount()), tw: tw, th: th, pad: pad, bytes: int64(bw) * int64(bh) * 4, mem: dec.retained()}
	a.delay = make([]int32, n)
	for i := range a.delay {
		a.delay[i] = effDelay(dec.delay(i))
	}
	return result{buf: buf, w: tw, h: th, pad: pad, frames: int32(n), dlay: a.delay[0]}, a, true
}

// decodeFrame decodes a later frame of an animation.
func (w *worker) decodeFrame(c *Cache, j *job) result {
	a := j.anim
	bw, bh := int(a.tw+2*a.pad), int(a.th+2*a.pad)
	buf := c.bufs.get(bw*bh*4, &w.fr)
	if buf < 0 {
		return result{buf: -1, err: errTooLarge, seq: j.seq, epoch: j.epoch}
	}
	if err := a.dec.render(w, int(j.frame), a.tw, a.th, a.pad, c.bufs.bytes(buf, bw*bh*4), bw); err != nil {
		c.bufs.put(buf, &w.fr)
		return result{buf: -1, err: errDecode, seq: j.seq, epoch: j.epoch}
	}
	return result{buf: buf, w: a.tw, h: a.th, pad: a.pad, seq: j.seq, epoch: j.epoch}
}

// openDecoder builds the animation decoder for data, from the test hook or
// the system's.
func (w *worker) openDecoder(data []byte) (animDecoder, error) {
	if openAnimHook != nil {
		if d, err := openAnimHook(data); d != nil || err != nil {
			return d, err
		}
	}
	return w.openSystemDecoder(data)
}

// openGoDecoder opens the formats decoded in Go on every system: a GIF (the
// frames are composited with transparent disposal, as browsers do) and, off
// macOS, an APNG.
func openGoDecoder(data []byte, apng bool) (animDecoder, error) {
	switch sniff(data) {
	case "gif":
		g, err := openGIF(data)
		if err != nil {
			return nil, err
		}
		return stdAnim{g}, nil
	case "png":
		if !apng {
			return nil, errNotAnimated
		}
		p, err := openAPNG(data)
		if err != nil {
			return nil, err
		}
		return stdAnim{p}, nil
	}
	return nil, errNotAnimated
}
