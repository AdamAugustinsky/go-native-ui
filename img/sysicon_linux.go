//go:build linux

package img

import (
	"bufio"
	"image"
	"image/png"
	"os"
)

// iconDecoder has no state on Linux.
type iconDecoder struct{}

// decodeIcon decodes the icon theme's PNG for the icon src names (the
// lookup is in sysicon_xdg.go); a system without a MIME database, a theme
// or a PNG for it fails with ErrUnsupported.
func (w *worker) decodeIcon(c *Cache, j *job) result {
	fail := func(code uint8) result { return result{buf: -1, err: code} }
	side, code := iconSide(j.w, j.h, j.flags)
	if code != errNone {
		return fail(code)
	}
	path, code := xdgSystem().iconPNG(j.src, int(side))
	if code != errNone {
		return fail(code)
	}
	f, err := os.Open(path)
	if err != nil {
		return fail(errRead)
	}
	defer f.Close()
	br := bufio.NewReader(f)
	cfg, err := png.DecodeConfig(br)
	if err != nil {
		return fail(errDecode)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return fail(errTooLarge)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fail(errRead)
	}
	br.Reset(f)
	var m image.Image
	if m, err = png.Decode(br); err != nil {
		return fail(errDecode)
	}
	return w.iconFromImage(c, m, side)
}
