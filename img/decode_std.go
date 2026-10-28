//go:build !darwin

package img

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
	"os"

	_ "golang.org/x/image/webp" // registers the WebP decoder (lossy, lossless, alpha; C6)
)

// osDecoder holds what the system icons need (Windows); the standard
// library's decoders allocate their own images (on the worker).
type osDecoder struct{ icon iconDecoder }

// decode decodes the image in data (or the file j.src.path) with the
// standard library and scales it to its display size by area averaging,
// applying a JPEG's EXIF orientation.
func (w *worker) decode(c *Cache, j *job, data []byte) result {
	fail := func(code uint8) result { return result{buf: -1, err: code} }
	if j.src.kind == srcFile {
		b, err := os.ReadFile(j.src.path)
		if err != nil {
			return fail(errRead)
		}
		data = b
	}
	if len(data) == 0 {
		return fail(errDecode)
	}
	format := sniff(data)
	if format == "heif" {
		return fail(errUnsupported)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if errors.Is(err, image.ErrFormat) {
			return fail(errUnsupported)
		}
		return fail(errDecode)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return fail(errTooLarge)
	}
	if j.frame != 0 {
		return fail(errDecode) // later frames are the animation's (anim_job.go)
	}
	frames, delay := int32(1), int32(0)
	m, code := decodeFirst(data, format, cfg)
	if code != errNone {
		return fail(code)
	}
	orient := 1
	switch format {
	case "jpeg":
		orient = Orientation(data)
	case "gif":
		frames, delay = gifFrames(data)
	case "webp":
		frames, delay = webpFrames(data)
	}
	sw, sh := int32(m.Bounds().Dx()), int32(m.Bounds().Dy())
	ow, oh := sw, sh
	if orient >= 5 {
		ow, oh = sh, sw
	}
	tw, th := scaled(ow, oh, fitScale(ow, oh, j.w, j.h, j.flags&Cover != 0))
	if tw > maxSide || th > maxSide {
		return fail(errTooLarge)
	}
	pad := atlasPad(tw, th)
	bw, bh := int(tw+2*pad), int(th+2*pad)
	buf := c.bufs.get(bw*bh*4, &w.fr)
	if buf < 0 {
		return fail(errTooLarge)
	}
	pix := c.bufs.bytes(buf, bw*bh*4)
	resample(pix[(int(pad)*bw+int(pad))*4:], bw*4, int(tw), int(th), m, orient)
	if pad > 0 {
		padEdges(pix, int(tw), int(th))
	}
	return result{buf: buf, w: tw, h: th, pad: pad, frames: frames, dlay: delay}
}

// decodeFirst decodes the first frame of data, of the sniffed format and
// configuration cfg, at the size of its canvas: a GIF's first frame on its
// logical screen; an animated WebP's first frame composited on its canvas
// (x/image/webp decodes only stills).
func decodeFirst(data []byte, format string, cfg image.Config) (image.Image, uint8) {
	if format == "webp" && webpAnimated(data) {
		a, err := openWebP(data)
		if err != nil {
			if errors.Is(err, errAnimLarge) {
				return nil, errTooLarge
			}
			return nil, errDecode
		}
		m, err := a.frame(0)
		if err != nil {
			return nil, errDecode
		}
		return m, errNone
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errDecode
	}
	if format == "gif" {
		m = onCanvas(m, cfg.Width, cfg.Height, data)
	}
	return m, errNone
}

// webpFrames is the frame count and the first frame's delay of a WebP: 1
// and 0 for a still one.
func webpFrames(data []byte) (frames, delay int32) {
	if !webpAnimated(data) {
		return 1, 0
	}
	a, err := openWebP(data)
	if err != nil {
		return 1, 0
	}
	return int32(a.count()), int32(a.delay(0))
}

// onCanvas places a GIF's first frame, decoded with the frame's own bounds,
// on the image's w×h logical screen at its offset: over the global palette's
// background colour, or a transparent screen when the frame has a
// transparent colour (gifBackground).
func onCanvas(m image.Image, w, h int, gif []byte) image.Image {
	canvas := image.Rect(0, 0, w, h)
	if m.Bounds() == canvas {
		return m
	}
	out := image.NewRGBA(canvas)
	if bg, ok := gifBackground(gif); ok {
		draw.Draw(out, canvas, &image.Uniform{color.RGBA{bg[0], bg[1], bg[2], 255}}, image.Point{}, draw.Src)
	}
	r := m.Bounds().Intersect(canvas)
	draw.Draw(out, r, m, r.Min, draw.Over)
	return out
}

// decodeImage is Decode off macOS: the first frame of data through the
// standard library, scaled by area averaging to fit limit×limit (0: full
// size), with a JPEG's EXIF orientation applied.
func decodeImage(data []byte, limit int32) (*image.RGBA, uint8) {
	format := sniff(data)
	if format == "heif" {
		return nil, errUnsupported
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if errors.Is(err, image.ErrFormat) {
			return nil, errUnsupported
		}
		return nil, errDecode
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, errTooLarge
	}
	m, code := decodeFirst(data, format, cfg)
	if code != errNone {
		return nil, code
	}
	orient := 1
	if format == "jpeg" {
		orient = Orientation(data)
	}
	sw, sh := int32(m.Bounds().Dx()), int32(m.Bounds().Dy())
	ow, oh := sw, sh
	if orient >= 5 {
		ow, oh = sh, sw
	}
	tw, th := scaled(ow, oh, fitScale(ow, oh, limit, limit, false))
	if tw > maxSide || th > maxSide {
		return nil, errTooLarge
	}
	out := image.NewRGBA(image.Rect(0, 0, int(tw), int(th)))
	resample(out.Pix, out.Stride, int(tw), int(th), m, orient)
	return out, errNone
}
