package rex

import (
	"math"
	"unsafe"

	"gonativeui/text"
	"gonativeui/theme"
	rextheme "gonativeui/theme/rex"
	"gonativeui/ui"
)

// What the scene needs besides the theme: text and icon boxes placed by
// baseline and centre, and the scale-dependent setup.

// fl makes a box a floating child of its container, placed by NextPos.
const fl = theme.Floating

// SetScale (re)creates everything that depends on the backing scale: the
// resolved theme (fonts, metrics, a fresh chrome), the terminal cell grid and
// the icons. It keeps the light/dark mix and the accent of the theme it
// replaces. It allocates; call it on the thread that owns the text system.
func (r *Rex) SetScale(s float32) {
	r.Scale, r.K = s, s/2
	t, acc := r.th.T, r.accent
	r.th = rextheme.Theme().Resolve(r.ts, s, theme.Light)
	r.th.SetAccent(acc)
	r.th.Mix(t)
	r.cells = rextheme.NewGrid(r.ts, s)
	r.icons.Raster(s)
}

// CellAdvancePt, RowPitchPt, MonoFace and MonoSizePt describe the terminal
// grid in use.
func (r *Rex) CellAdvancePt() float64 { return r.cells.AdvPt() }

// RowPitchPt is the height of a terminal row in points.
func (r *Rex) RowPitchPt() float64 { return r.cells.RowPitchPt() }

// MonoFace names the monospace face of the grid.
func (r *Rex) MonoFace() string { return r.cells.Face }

// MonoSizePt is the size in points of the grid's monospace font.
func (r *Rex) MonoSizePt() float64 { return float64(r.cells.SizePt) }

// strBytes views s as bytes without copying.
func strBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

// Width is the advance of s in font f.
func (r *Rex) Width(f text.FontKey, s string) float32 {
	return r.ts.Run(r.ts.Shape(f, strBytes(s))).Advance
}

// met is the line box of f, one of the theme's fonts or the grid's.
func (r *Rex) met(f text.FontKey) theme.FontMet {
	switch f {
	case r.cells.Mono:
		return r.cells.Met
	case r.cells.MonoBold:
		return r.cells.BoldMet
	}
	return r.th.Met(f)
}

// Put places the text s with its pen origin at (x, baseline) in the
// coordinates of the current parent. width > 0 fixes the box width (text
// wider than that fades out, with ui.FadeOverflowX in extra).
func (r *Rex) Put(c *ui.Context, kg *keyGen, f text.FontKey, s string, x, baseline float32, color uint32, width float32, extra ui.Flags) {
	r.PutBytes(c, kg, f, strBytes(s), x, baseline, color, width, extra)
}

// PutBytes is Put for a byte slice, which must stay valid until the frame ends.
func (r *Rex) PutBytes(c *ui.Context, kg *keyGen, f text.FontKey, s []byte, x, baseline float32, color uint32, width float32, extra ui.Flags) {
	fm := r.met(f)
	ix := float32(math.Floor(float64(x)))
	frac := x - ix
	c.NextFont(f)
	c.NextTextColor(color)
	c.NextPos(ix, baseline-fm.Off)
	c.NextPadding(frac, 0, 0, 0)
	if width > 0 {
		c.NextSize(ui.Px(width+frac), ui.Px(fm.H))
	} else {
		c.NextSize(ui.TextSize(0), ui.Px(fm.H))
	}
	c.BoxKeyed(ui.DrawText|fl|extra, kg.Next(), s)
}

// IconAt builds a box that shows icon id centred at (cx, cy) in the parent's
// coordinates, in colour col (RGBA).
func (r *Rex) IconAt(c *ui.Context, kg *keyGen, id iconID, cx, cy float32, color uint32) {
	flags, disp := r.icons.Prepare(c, id, color)
	w, h := r.icons.Size(id)
	theme.At(c, theme.Snap(cx-w/2), theme.Snap(cy-h/2), w, h)
	c.BoxKeyed(flags|fl, kg.Next(), disp)
}

// IconButton builds a square hit box of side hit centred at (cx, cy) with
// icon id centred in it. The icon goes from colour base to hover as the
// pointer comes in, over a faint rounded background. A disabled button is
// drawn faded and takes no input.
func (r *Rex) IconButton(c *ui.Context, key ui.Key, id iconID, cx, cy, hit float32, base, hover uint32, enabled bool) {
	t := c.HotTOf(key)
	pressed := c.ActiveTOf(key)
	flags := ui.DrawBackground | fl
	bx, by := theme.Snap(cx-hit/2), theme.Snap(cy-hit/2)
	if enabled {
		flags |= ui.Clickable | ui.HotAnimation | ui.ActiveAnimation
		c.NextCursor(ui.CursorPointer)
		c.NextBG(theme.ColA(0x2A2030, 0.08*t+0.07*pressed))
	} else {
		c.PushOpacity(0.4)
	}
	theme.At(c, bx, by, hit, hit)
	c.NextRadius(6 * r.Scale)
	btn := c.BoxKeyed(flags, key, nil)
	if !enabled {
		c.PopOpacity()
	}
	// The icon is placed against the parent's grid, not the button's.
	c.Push(btn)
	kg := keyGen{Base: ui.KeyN(key, 1000)}
	if !enabled {
		c.PushOpacity(0.4)
	}
	r.IconAt(c, &kg, id, cx-bx, cy-by, theme.Col(theme.MixRGB(base, hover, max(t, pressed))))
	if !enabled {
		c.PopOpacity()
	}
	c.Pop()
}
