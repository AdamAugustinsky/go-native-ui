package rex

import (
	"math"
	"unsafe"

	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	rextheme "gonativeui/theme/rex"
	"gonativeui/ui"
)

// What the scene needs besides the theme: text and icon boxes placed by
// baseline and centre, and the scale-dependent setup.

// fl makes a box a floating child of its container, placed by NextPos.
const fl = theme.Floating

// SetScale (re)creates the terminal cell grid for the backing scale. The
// kit recreates the resolved theme (fonts, metrics, a fresh chrome) and the
// icons in the next frame's Begin, keeping the light/dark mix. It allocates;
// call it on the thread that owns the text system.
func (r *Rex) SetScale(s float32) {
	r.Scale, r.K = s, s/2
	r.cells = rextheme.NewGrid(r.ts, s)
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

// tone is the colour (0xRRGGBB) between a light and a dark one at the theme's
// mix of the two modes: the light one at T = 0, the dark one at T = 1.
func (r *Rex) tone(light, dark uint32) uint32 { return theme.MixRGB(light, dark, r.th.T) }

// col is tone as an opaque 0xRRGGBBAA colour.
func (r *Rex) col(light, dark uint32) uint32 { return theme.Col(r.tone(light, dark)) }

// colA is tone with alpha: lightA in the light mode, darkA in the dark one.
func (r *Rex) colA(light, dark uint32, lightA, darkA float32) uint32 {
	return theme.ColA(r.tone(light, dark), theme.Lerp(lightA, darkA, r.th.T))
}

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
	flags, disp := r.k.Icons().Prepare(c, id, color)
	w, h := r.k.IconSize(id)
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
		c.NextBG(theme.ColA(r.tone(0x2A2030, 0xFFFFFF), 0.08*t+0.07*pressed))
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

// headerButtons are the pane header buttons' accessible names, by button.
var headerButtons = [numBtns]string{btnSplitX: "Split right", btnSplitY: "Split down", btnMax: "Maximise", btnClose: "Close"}

// HeaderButton builds a pane header button: a rexui.IconButton (borderless)
// the size of the hit box, centred at (cx, cy), inside a holder that draws
// the hover and press chip behind it. Rex's greys come from roles the scene
// owns (see setHeaderRecipe); a disabled button is faded and takes no input.
func (r *Rex) HeaderButton(c *ui.Context, holder ui.Key, id iconID, name string, cx, cy, hit float32, enabled bool) {
	inner := ui.HashString(holder, name)
	bx, by := theme.Snap(cx-hit/2), theme.Snap(cy-hit/2)
	bg := uint32(0)
	if enabled {
		t, pressed := c.HotTOf(inner), c.ActiveTOf(inner)
		bg = theme.ColA(r.tone(0x2A2030, 0xFFFFFF), 0.08*t+0.07*pressed)
	}
	theme.At(c, bx, by, hit, hit)
	c.NextBG(bg)
	c.NextRadius(6 * r.Scale)
	box := c.BoxKeyed(ui.DrawBackground|fl, holder, nil)
	c.Push(box)
	// The icon sits where the scene's grid puts it (centred on (cx, cy), snapped
	// to a pixel). The button centres it in its own box, so the box is at a
	// whole pixel, as large as the hit box but for a pixel of parity with the
	// icon's size, and has the icon's centre as its own.
	iw, ih := r.k.IconSize(id)
	w, h := fitParity(theme.Snap(hit), iw), fitParity(theme.Snap(hit), ih)
	x, y := theme.Snap(cx-iw/2)-bx+(iw-w)/2, theme.Snap(cy-ih/2)-by+(ih-h)/2
	theme.At(c, x, y, w, h)
	c.NextFlags(fl)
	r.k.BeginDisabled(!enabled)
	r.k.NextSize(ui.Px(w), ui.Px(h))
	if enabled {
		c.NextCursor(ui.CursorPointer) // Rex's buttons show the hand
	}
	rexui.IconButton(r.k, id, name, rexui.IconButtonOpts{Variant: rexui.IconBorderless})
	r.k.EndDisabled()
	c.Pop()
}

// fitParity is the side n, or one pixel less if that makes n - m even.
func fitParity(n, m float32) float32 {
	if math.Mod(float64(n-m), 2) != 0 {
		return n - 1
	}
	return n
}

// setHeaderRecipe gives the borderless icon button Rex's pane-header greys: a
// quiet grey that darkens to the text colour under the pointer. The scene has
// no sidebar, so two of the theme's own sidebar roles carry the colours
// (headerTheme sets them); rexui and theme/rex should own a role for it.
func setHeaderRecipe() {
	rc := &rexui.IconButtonRecipes[rexui.IconBorderless]
	rest := rexui.Of(theme.SidebarLabel)
	rc.Label, rc.LabelInactive, rc.LabelPressed = rest, rest, rexui.Of(theme.SidebarHeader)
	rc.LabelDisabled = rexui.OfAlpha(theme.SidebarLabel, 0.4, 0.4)
	rexui.IconButtonHoverLabel = rexui.Of(theme.SidebarHeader)
}

// headerTheme is the Rex theme with the scene's own pieces: the pane title's
// text style (12.75 pt semibold, the reference's) and the header buttons'
// greys.
func headerTheme() *theme.Theme {
	t := rextheme.Theme()
	t.Text = theme.MacText
	t.Text[theme.StylePaneTitle] = theme.TextSpec{Size: 12.75, Line: 16, Weight: theme.WeightSemibold}
	l, d := &t.Roles[theme.Light], &t.Roles[theme.Dark]
	l[theme.SidebarLabel], d[theme.SidebarLabel] = theme.Col(0x96999B), theme.Col(0x8A8EA1)
	l[theme.SidebarHeader], d[theme.SidebarHeader] = theme.Col(0x3F4246), theme.Col(0xECEDF3)
	return t
}
