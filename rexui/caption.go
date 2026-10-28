package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// CaptionRecipe is the look of the caption buttons a drawn titlebar shows
// (CaptionButtons): Windows 11's own, as WinUI's title bar has them, so a
// Rex window on Windows keeps the system's minimise, maximise and close as a
// macOS one keeps its traffic lights.
type CaptionRecipe struct {
	// Glyph colours the symbols (TextFillColorPrimary), GlyphInactive in an
	// inactive window (TextFillColorDisabled).
	Glyph, GlyphInactive Pair
	// Hover and Press fill minimise and maximise under the pointer and
	// while pressed (SubtleFillColorSecondary, SubtleFillColorTertiary).
	Hover, Press Pair
	// CloseHover and ClosePress fill close (#C42B1C, and at 90 %), whose
	// symbol is then CloseGlyph and CloseGlyphPress.
	CloseHover, ClosePress      Pair
	CloseGlyph, CloseGlyphPress Pair
}

// CaptionLook is the caption buttons' recipe.
var CaptionLook = CaptionRecipe{
	Glyph: PairA(0x000000, 0xFFFFFF, 0.894, 1), GlyphInactive: PairA(0x000000, 0xFFFFFF, 0.361, 0.365),
	Hover: PairA(0x000000, 0xFFFFFF, 0.035, 0.06), Press: PairA(0x000000, 0xFFFFFF, 0.024, 0.04),
	CloseHover: Solid(0xC42B1C, 0xC42B1C), ClosePress: PairA(0xC42B1C, 0xC42B1C, 0.9, 0.9),
	CloseGlyph: Solid(0xFFFFFF, 0xFFFFFF), CloseGlyphPress: PairA(0xFFFFFF, 0xFFFFFF, 0.7, 0.7),
}

var keyCaption = ui.HashString(0, "rexui.caption")

// CaptionButtons draws the window's minimise, maximise (restore, when
// maximized) and close buttons where the OS hit-tests them
// (ui.Context.SystemButtons), when the app draws them
// (ui.Decorations.Caption: Windows with app.Config.Titlebar), and asks the
// window for the action of the one clicked. It does nothing otherwise. Call it
// in a box at the window's origin (the toolbar's band), after the band's drag
// area: BeginToolbar does, for the window's band.
func CaptionButtons(k *base.Kit) {
	c := k.Context()
	d := c.Decorations()
	sb := c.SystemButtons()
	if !d.Caption || sb[2] <= sb[0] || sb[3] <= sb[1] {
		return
	}
	rc, t := &CaptionLook, k.Theme().T
	inactive := k.Inactive()
	bw := (sb[2] - sb[0]) / 3
	for i := 0; i < 3; i++ {
		key := ui.KeyN(keyCaption, uint64(i))
		hot, press := c.HotTOf(key), c.ActiveTOf(key)
		x0 := theme.Snap(sb[0] + float32(i)*bw)
		x1 := theme.Snap(sb[0] + float32(i+1)*bw)
		if i == 2 {
			x1 = sb[2]
		}
		var bg, fg uint32
		glyph, name, act := icon.Minimise, str.Menu.Minimize, ui.ActionMinimise
		switch i {
		case 1:
			glyph, name, act = icon.Maximise, str.Maximize, ui.ActionToggleMaximise
			if d.Maximized {
				glyph, name = icon.Restore, str.Restore
			}
		case 2:
			glyph, name, act = icon.Close, str.Menu.Close, ui.ActionClose
		}
		fg = rc.Glyph.At(t)
		if inactive {
			fg = rc.GlyphInactive.At(t)
		}
		if i == 2 {
			bg = theme.WithAlpha(rc.CloseHover.At(t), max(hot, press))
			if press > 0 {
				bg = theme.MixRGBA(bg, rc.ClosePress.At(t), press)
			}
			if h := max(hot, press); h > 0 {
				fg = theme.MixRGBA(fg, rc.CloseGlyph.At(t), h)
				if press > 0 {
					fg = theme.MixRGBA(fg, rc.CloseGlyphPress.At(t), press)
				}
			}
		} else {
			bg = theme.WithAlpha(rc.Hover.At(t), hot)
			if press > 0 {
				bg = theme.MixRGBA(bg, rc.Press.At(t), press)
			}
		}
		theme.At(c, x0, sb[1], x1-x0, sb[3]-sb[1])
		c.NextBG(bg)
		c.NextCursor(ui.CursorArrow)
		c.NextRole(ui.RoleButton)
		c.NextSemName(name)
		id := c.BoxKeyed(ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|ui.DrawBackground|base.Floating, key, nil)
		if c.Signal(id).Clicked {
			c.RequestWindowAction(act)
		}
		c.Push(id)
		if flags, disp := k.Icons().Prepare(c, glyph, fg); flags != 0 {
			gw, gh := k.Icons().Size(glyph)
			c.NextPos(theme.Snap((x1-x0-gw)/2), theme.Snap((sb[3]-sb[1]-gh)/2))
			c.BoxKeyed(flags|base.Floating, ui.KeyN(key, 1), disp)
		}
		c.Pop()
	}
}
