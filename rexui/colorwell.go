package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ColorWellState is a colour well's state, owned by the app.
type ColorWellState struct {
	// Open: the drawn picker shows.
	Open bool
	// Hex is the picker's hex field. Call Hex.Reserve(16) once to make typing
	// in it allocation-free.
	Hex ui.TextBuffer
}

// ColorWellOpts are a colour well's options, passed by value.
type ColorWellOpts struct {
	// Panel makes the well ask for the system's colour panel (NSColorPanel on
	// macOS) rather than open its own picker: ColorWellSignal.Panel is set when
	// it is pressed, and the app shows the panel and writes the colour the
	// panel reports to the well's value. There is no platform call for the
	// panel yet (an N2-style request, to be added to platform and app), so
	// until there is, leave this off: the drawn picker is complete.
	Panel bool
}

// ColorWellRecipe is the look of colour wells (NSColorWell with the default
// style on macOS 27.2, measured on the Mac mini): a 48 × 24 capsule bezel in
// the system fill with the colour in a capsule inset 3 pt, which a
// darker (lighter, in dark) rim outlines.
type ColorWellRecipe struct {
	// W and H are the well's size, Inset the swatch's distance from the bezel
	// and RimW the rim's width, in points.
	W, H, Inset, RimW float32
	// Bezel is the bezel's fill (the system fill: #E6E6E6 over white, #343434
	// over the dark window, measured); Pressed the bezel while it is held and
	// BezelOff a disabled well's (#F2F2F2 and #292929 measured: the tertiary
	// fill).
	Bezel, Pressed, BezelOff Tint
	// Rim outlines the swatch: black at 17 % in light, white at 13 % in dark
	// over the colour (measured on a teal swatch).
	Rim Tint
	// SwatchRadius is the swatch's corner radius, in points (8.5 to 8.7
	// measured, a little under half its 18 pt height).
	SwatchRadius float32
	// Off is how far a disabled well's swatch moves toward the bezel: a colour
	// mix, not an opacity.
	Off float32
	// Picker's geometry, in points: the popover's Width, the SwatchD of a
	// swatch and the Gap between swatches; PerRow swatches to a row.
	Width, SwatchD, Gap float32
	PerRow              int
}

// ColorWellLook is the colour wells' recipe.
var ColorWellLook = ColorWellRecipe{
	W: 48, H: 24, Inset: 3, RimW: 1,
	Bezel: Of(theme.Fill1), Pressed: Of(theme.FillPressed), BezelOff: Of(theme.Fill3), Rim: OfAlpha(theme.Label, 0.17, 0.13),
	SwatchRadius: 8.5, Off: 0.55,
	Width: 216, SwatchD: 24, Gap: 8, PerRow: 6,
}

// pickerColors are the system colours the drawn picker offers, with their names
// for assistive technologies.
var pickerColors = [...]struct {
	role theme.Role
	name string
}{
	{theme.Red, "Red"}, {theme.Orange, "Orange"}, {theme.Yellow, "Yellow"}, {theme.Green, "Green"}, {theme.Mint, "Mint"}, {theme.Teal, "Teal"},
	{theme.Cyan, "Cyan"}, {theme.Blue, "Blue"}, {theme.Indigo, "Indigo"}, {theme.Purple, "Purple"}, {theme.Pink, "Pink"}, {theme.Brown, "Brown"},
}

// ColorWellSignal is what a colour well reports.
type ColorWellSignal struct {
	base.Signal
	// Open: the drawn picker is showing.
	Open bool
	// Panel: the well was pressed with ColorWellOpts.Panel on: the app shows
	// the system's colour panel.
	Panel bool
}

// ColorWell builds a colour well editing *rgba (0xRRGGBBAA): a bezel holding the
// colour, which opens a picker when pressed (Space, Enter or a click) — a popover
// of the system colours with a hex field (#RRGGBB, Enter applies it). Changed
// is set in the frame the colour changed. The accessible name is the label and
// the value the colour's hex.
//
//	if rexui.ColorWell(k, "Accent", &v.well, &v.accent, rexui.ColorWellOpts{}).Changed { v.apply() }
func ColorWell(k *base.Kit, label string, st *ColorWellState, rgba *uint32, o ColorWellOpts) ColorWellSignal {
	c, r := k.Context(), k.Theme()
	rc := &ColorWellLook
	sc := k.Scale()
	p := k.PressState(label, base.HoverOff)
	w, h := pt(sc, rc.W), pt(sc, rc.H)
	radius := h / 2
	inset := pt(sc, rc.Inset)

	bezel := rc.Bezel.RGBA(r)
	if p.Disabled {
		bezel = rc.BezelOff.RGBA(r)
	} else if p.Down > 0 || st.Open {
		bezel = mix(bezel, rc.Pressed.RGBA(r), max(p.Down, 0.5*b2f(st.Open)))
	}
	swatch := theme.Over(*rgba, r.Colors[theme.Window]|0xFF)
	rim := rc.Rim.RGBA(r)
	if p.Disabled {
		swatch = theme.MixRGBA(swatch, theme.Over(bezel, r.Colors[theme.Window]|0xFF), rc.Off)
		rim = 0
	}
	f := c.Fmt()
	hexOf(f, *rgba)
	c.NextBG(bezel)
	c.NextRadius(radius)
	c.NextSemValueBytes(f.Bytes())
	c.NextSemState(openState(st.Open))
	id := k.BeginPress(&p, ui.RoleButton, ui.Px(w), ui.Px(h), ui.DrawBackground, nil)
	// the swatch: the rim's colour in a capsule, the colour in one a rim's
	// width smaller (a darker, or in dark mode lighter, outline over it)
	sw, sh, rimW := w-2*inset, h-2*inset, pt(sc, rc.RimW)
	sr := pt(sc, rc.SwatchRadius)
	if rim != 0 {
		theme.At(c, inset, inset, sw, sh)
		c.NextBG(theme.Over(rim, swatch))
		c.NextRadius(sr)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 2), nil)
	}
	theme.At(c, inset+rimW*b2f(rim != 0), inset+rimW*b2f(rim != 0), sw-2*rimW*b2f(rim != 0), sh-2*rimW*b2f(rim != 0))
	c.NextBG(swatch)
	c.NextRadius(max(sr-rimW*b2f(rim != 0), 0))
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 1), nil)
	k.FocusRing(id, p.Visual, radius)
	sig := ColorWellSignal{Signal: k.EndPress(&p)}

	if !p.Disabled && sig.Clicked {
		switch {
		case o.Panel:
			sig.Panel = true
		case st.Open:
			st.Open = false
		default:
			st.Open = true
			st.Hex.SetBytes(hexBytes(c, *rgba))
		}
	}
	if st.Open && !o.Panel && !p.Disabled {
		anchor := c.Rect(id)
		pop := BeginPopover(k, "picker", anchor, PopoverOpts{Width: rc.Width, Label: "Colour"})
		if colorPicker(k, st, rgba) {
			sig.Changed = true
		}
		EndPopover(k, &pop)
		if pop.Dismissed {
			st.Open = false
		}
	}
	sig.Open = st.Open
	return sig
}

// colorPicker builds the picker's content in the popover: rows of swatches and
// a hex field. It reports whether it changed *rgba.
func colorPicker(k *base.Kit, st *ColorWellState, rgba *uint32) (changed bool) {
	c, r := k.Context(), k.Theme()
	rc := &ColorWellLook
	sc := k.Scale()
	d := pt(sc, rc.SwatchD)
	k.NextGap(rc.Gap)
	k.Column("picker")
	for row := 0; row*rc.PerRow < len(pickerColors); row++ {
		k.NextGap(rc.Gap)
		k.Row(c.Fmt().Str("row").Int(row).String())
		for i := row * rc.PerRow; i < min((row+1)*rc.PerRow, len(pickerColors)); i++ {
			pc := &pickerColors[i]
			col := r.Colors[pc.role] | 0xFF
			p := k.PressState(pc.name, base.HoverOff)
			selected := *rgba>>8 == col>>8
			c.NextBG(col)
			c.NextRadius(d / 2)
			if selected {
				c.NextBorder(r.Colors[theme.Label], pt(sc, 2))
				c.NextSemState(ui.SemSelected)
			}
			fl := ui.DrawBackground
			if selected {
				fl |= ui.DrawBorder
			}
			k.BeginPress(&p, ui.RoleRadio, ui.Px(d), ui.Px(d), fl, nil)
			k.FocusRing(p.ID, p.Visual, d/2)
			if k.EndPress(&p).Clicked && !selected {
				*rgba = col
				st.Hex.SetBytes(hexBytes(c, col))
				changed = true
			}
		}
		k.EndRow()
	}
	k.NextWidth(ui.Pct(1))
	fs := TextField(k, "Hex", &st.Hex, "#RRGGBB", FieldOpts{Size: theme.SizeSmall})
	if fs.Submitted {
		if v, ok := parseHex(st.Hex.Bytes()); ok && v != *rgba {
			*rgba = v
			changed = true
		}
	}
	k.EndColumn()
	return changed
}

// hexOf appends col as "#RRGGBB" to f.
func hexOf(f *ui.Fmt, col uint32) {
	const digits = "0123456789ABCDEF"
	f.Byte('#')
	for s := 20; s >= 0; s -= 4 {
		f.Byte(digits[col>>(uint(s)+8)&0xF])
	}
}

// hexBytes is col as "#RRGGBB" in the frame's arena.
func hexBytes(c *ui.Context, col uint32) []byte {
	f := c.Fmt()
	hexOf(f, col)
	return f.Bytes()
}

// parseHex reads "#RRGGBB" or "RRGGBB" (also three digits) as an opaque colour.
func parseHex(b []byte) (uint32, bool) {
	if len(b) > 0 && b[0] == '#' {
		b = b[1:]
	}
	if len(b) != 6 && len(b) != 3 {
		return 0, false
	}
	var v uint32
	for _, ch := range b {
		var d uint32
		switch {
		case ch >= '0' && ch <= '9':
			d = uint32(ch - '0')
		case ch >= 'a' && ch <= 'f':
			d = uint32(ch-'a') + 10
		case ch >= 'A' && ch <= 'F':
			d = uint32(ch-'A') + 10
		default:
			return 0, false
		}
		if len(b) == 3 {
			v = v<<8 | d<<4 | d
		} else {
			v = v<<4 | d
		}
	}
	return v<<8 | 0xFF, true
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// openState is SemExpanded for an open picker.
func openState(open bool) ui.SemState {
	if open {
		return ui.SemExpanded
	}
	return 0
}
