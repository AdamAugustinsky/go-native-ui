//go:build !darwin

package img

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
	"os"
)

// osDecoder has no state: the standard library's decoders allocate their
// own images (on the worker).
type osDecoder struct{}

// maxPixels bounds the images the standard decoders are asked to decode in
// full (a decompression bomb is refused before any pixel is allocated).
const maxPixels = 100 << 20

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
	if format == "webp" || format == "heif" {
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
		return fail(errDecode) // later frames come with the animation work (C6)
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fail(errDecode)
	}
	frames, delay := int32(1), int32(0)
	orient := 1
	switch format {
	case "jpeg":
		orient = exifOrientation(data)
	case "gif":
		frames, delay = gifFrames(data)
	}
	rgba, ok := m.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(m.Bounds())
		draw.Draw(rgba, rgba.Rect, m, m.Bounds().Min, draw.Src)
	}
	sw, sh := int32(rgba.Rect.Dx()), int32(rgba.Rect.Dy())
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
	resample(pix[(int(pad)*bw+int(pad))*4:], bw*4, int(tw), int(th), rgba, orient)
	if pad > 0 {
		padEdges(pix, int(tw), int(th))
	}
	return result{buf: buf, w: tw, h: th, pad: pad, frames: frames, dlay: delay}
}
