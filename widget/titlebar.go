package widget

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
)

// glyphs adapts the kit's icon atlas to theme.Glyphs, so that the caption
// buttons of a titlebar the app draws use the system's symbols.
type glyphs struct{ k *Kit }

var _ theme.Glyphs = (*glyphs)(nil)

// Glyph builds the symbol g centred at (cx, cy) of the current parent.
func (g *glyphs) Glyph(c *ui.Context, key ui.Key, gl theme.Glyph, cx, cy, size float32, color uint32) bool {
	var id icon.ID
	switch gl {
	case theme.GlyphClose:
		id = icon.Close
	case theme.GlyphMinimise:
		id = icon.Minimise
	case theme.GlyphMaximise:
		id = icon.Maximise
	case theme.GlyphRestore:
		id = icon.Restore
	default:
		return false
	}
	flags, disp := g.k.icons.Prepare(c, id, color)
	if flags == 0 {
		return false
	}
	w, h := g.k.icons.Size(id)
	c.NextPos(theme.Snap(cx-w/2), theme.Snap(cy-h/2))
	c.BoxKeyed(flags|fl, key, disp)
	return true
}

// Titlebar builds the window's titlebar band with the theme's chrome: a
// region that drags the window, the title, and the window buttons buttons asks
// for (theme.ButtonsCaption for windows that draw their own; the caption
// buttons use the system icons of the kit's atlas). Build it as a child of the
// window's root, over the background, before the content. maximised makes the
// maximise button a restore button. The result says which window button was
// clicked and where the band is free for tabs or a toolbar.
func (k *Kit) Titlebar(title string, buttons theme.ButtonStyle, maximised bool) theme.TitlebarResult {
	c := k.c
	w, _ := c.Size()
	return k.r.Chrome.Titlebar(c, k.r, theme.Titlebar{
		Width: float32(w), Title: title, Buttons: buttons, Maximised: maximised,
		Active: c.Input().Focused, Glyphs: &k.gl,
	})
}

// Background builds the window background of the theme (behind everything
// else).
func (k *Kit) Background() { k.r.Chrome.Background(k.c, k.r) }
