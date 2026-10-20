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

// mayAnimate reports whether the start of a file is of a format that can be
// an animation here: a GIF; a PNG with an animation control chunk; where the
// system decodes them, an animated WebP or a HEIF.
func mayAnimate(head []byte) bool {
	if openAnimHook != nil {
		return true
	}
	switch sniff(head) {
	case "gif":
		return true
	case "png":
		return bytes.Contains(head, []byte("acTL"))
	case "webp":
		return osAnimWebP && bytes.Contains(head, []byte("ANIM"))
	case "heif":
		return osAnimHEIF
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
		var head [4096]byte
		n, _ := io.ReadFull(f, head[:])
		if !mayAnimate(head[:n]) {
			return result{}, nil, false
		}
		st, err := f.Stat()
		if err != nil {
			return fail(errRead)
		}
		if st.Size() > animMaxFile {
			return fail(errTooLarge)
		}
		own = make([]byte, st.Size())
		copy(own, head[:n])
		if st.Size() > int64(n) {
			if _, err := io.ReadFull(f, own[n:]); err != nil {
				return fail(errRead)
			}
		}
	} else {
		if len(data) > animMaxFile {
			return fail(errTooLarge)
		}
		if !mayAnimate(data[:min(len(data), 4096)]) {
			return result{}, nil, false
		}
		own = append([]byte(nil), data...)
	}
	dec, err := w.openDecoder(own)
	if dec == nil {
		if err == nil || errors.Is(err, errNotAnimated) {
			return result{}, nil, false
		}
		return fail(errDecode)
	}
	n := dec.count()
	cw, ch := dec.size()
	if n <= 1 {
		dec.close(&w.fr)
		return result{}, nil, false
	}
	if cw <= 0 || ch <= 0 || int64(cw)*int64(ch) > animMaxCanvas || int64(cw)*int64(ch)*4*int64(n) > animMaxBytes || n > maxAnimFrames {
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
	a = &anim{dec: dec, frames: int32(n), plays: int32(dec.playCount()), tw: tw, th: th, pad: pad, bytes: int64(bw) * int64(bh) * 4}
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
		return result{buf: -1, err: errTooLarge}
	}
	if err := a.dec.render(w, int(j.frame), a.tw, a.th, a.pad, c.bufs.bytes(buf, bw*bh*4), bw); err != nil {
		c.bufs.put(buf, &w.fr)
		return result{buf: -1, err: errDecode}
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
