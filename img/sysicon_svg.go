//go:build linux

package img

import (
	"bytes"
	"image"
	"math"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// SVG icons for the freedesktop lookup (sysicon_xdg.go): most themes ship
// their icons as SVG only, so a lookup that stopped at PNG found nothing in
// them. The drawing is oksvg and rasterx (D2): a pure-Go parser and scanline
// rasteriser for the static SVG subset an icon theme uses (paths, shapes,
// gradients, transforms, fill and stroke, CSS classes), with no cgo and no
// system library. What it does not draw (filters, masks, text, clip paths)
// is skipped, not an error. Only Linux draws SVG icons (sysicon_linux.go), so
// the dependency is linked into Linux builds only: the other systems do not
// pay for it (+356 KB stripped on linux/amd64).

// maxSVGBytes is the largest SVG file read; a theme's icons are a few KB, and
// a file over this is ignored whole as the other files of the lookup are.
const maxSVGBytes = 2 << 20

// maxSVGExtent bounds the size a document may ask for in its viewBox or
// width and height, in user units; the picture is scaled to the side asked
// for whatever its units are, so this only rejects values that are not a
// drawing (NaN, infinity, 1e300).
const maxSVGExtent = 1e7

// rasterSVG draws the SVG document in data to fit side×side pixels, its
// aspect kept and centred, the rest transparent (premultiplied alpha). The
// error is errDecode for a document that does not parse, has no size or draws
// nothing it can show, errTooLarge for a side past the limits.
func rasterSVG(data []byte, side int) (m *image.RGBA, code uint8) {
	if side <= 0 || side > maxSide || int64(side)*int64(side) > maxPixels {
		return nil, errTooLarge
	}
	// The parser works on untrusted files; a malformed one must not take the
	// worker down.
	defer func() {
		if recover() != nil {
			m, code = nil, errDecode
		}
	}()
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.IgnoreErrorMode)
	if err != nil {
		return nil, errDecode
	}
	vw, vh := icon.ViewBox.W, icon.ViewBox.H
	if !(vw > 0 && vh > 0 && vw < maxSVGExtent && vh < maxSVGExtent) || math.IsNaN(icon.ViewBox.X+icon.ViewBox.Y) {
		return nil, errDecode
	}
	k := float64(side) / math.Max(vw, vh)
	w, h := vw*k, vh*k
	icon.SetTarget(float64(side)/2-w/2, float64(side)/2-h/2, w, h)
	m = image.NewRGBA(image.Rect(0, 0, side, side))
	scan := rasterx.NewScannerGV(side, side, m, m.Bounds())
	icon.Draw(rasterx.NewDasher(side, side, scan), 1)
	return m, errNone
}
