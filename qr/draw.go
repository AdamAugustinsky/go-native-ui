package qr

import (
	"math"

	"gonativeui/ui"
)

// QuietZone is the width, in modules, of the light border the standard
// requires around a symbol for scanners to find it.
const QuietZone = 4

// fit places the symbol of n modules (without the quiet zone) in a square of
// side pixels: the size of a module in whole pixels and the offset, in pixels,
// of the quiet zone's top left corner from the square's. A module is a whole
// number of pixels, so every edge lies on a pixel boundary and the symbol is
// crisp at any scale factor; the square's leftover (less than one module per
// module of the symbol) is split evenly around it. A square too small for a
// pixel per module gets a fractional module size, which no scanner will read.
func fit(n int, side float32) (module, offset float32) {
	total := float32(n + 2*QuietZone)
	side = float32(math.Floor(float64(side)))
	if side < total {
		return side / total, 0
	}
	module = float32(math.Floor(float64(side / total)))
	return module, float32(math.Floor(float64((side - module*total) / 2)))
}

// Draw builds the symbol in c as a square box of size device pixels on a side,
// filled with bg, and one box of fg for each horizontal run of dark modules:
// the quiet zone is part of the square. Modules are a whole number of pixels
// across, as many as fit (so the symbol is crisp at 1× and 2×; pick size as
// a multiple of Size()+2·QuietZone to fill the square exactly), and the symbol
// is centred in what is left. fg and bg are RGBA8 (0xRRGGBBAA); scanners want
// dark on light, with plenty of contrast. key identifies the box among its
// siblings. It returns the square's box, so that a caller who wants it
// announced builds NextRole and NextSemName before the call (they apply to
// the square, the first box Draw builds).
//
// A symbol of version 10 (57 modules) is a few hundred boxes. Once the
// context's box cache has grown to hold them, drawing allocates nothing.
//
// (In the component library this is rexui.QRCode, which supplies the colours
// from the theme; it will call Draw.)
func Draw(c *ui.Context, key ui.Key, m *Code, size float32, fg, bg uint32) ui.ID {
	side := float32(math.Floor(float64(size)))
	c.NextSize(ui.Px(side), ui.Px(side))
	c.NextBG(bg)
	id := c.BoxKeyed(ui.DrawBackground, key, nil)
	n := m.Size()
	if n == 0 {
		return id
	}
	c.Push(id)
	mod, off := fit(n, side)
	origin := off + QuietZone*mod
	k := uint64(1)
	for y := 0; y < n; y++ {
		for x := 0; x < n; {
			if !m.get(x, y) {
				x++
				continue
			}
			x0 := x
			for x < n && m.get(x, y) {
				x++
			}
			c.NextPos(origin+float32(x0)*mod, origin+float32(y)*mod)
			c.NextSize(ui.Px(float32(x-x0)*mod), ui.Px(mod))
			c.NextBG(fg)
			c.BoxKeyed(ui.DrawBackground|ui.FloatingX|ui.FloatingY, ui.KeyN(key, k), nil)
			k++
		}
	}
	c.Pop()
	return id
}
