package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// SearchRecipe is the look of search fields (NSSearchField on macOS 27.2): a
// capsule in FieldLook's colours, a magnifying glass at its start, and while
// it holds text a filled-circle clear button at its end. It builds on
// textfield.go.
type SearchRecipe struct {
	// IconX is the magnifying glass's centre from the start, TextX the
	// text's start, and ClearRight the clear button's centre from the end,
	// in points (13.75, 24 and 12 measured at regular size).
	IconX, TextX, ClearRight float32
	// Icon colours the magnifying glass; Clear the clear button
	// (secondaryLabelColor, #757575 measured), ClearHover and ClearPressed
	// it under the pointer and pressed.
	Icon, Clear, ClearHover, ClearPressed Tint
}

// SearchLook is the search fields' recipe.
var SearchLook = SearchRecipe{
	IconX: 13.75, TextX: 24, ClearRight: 12,
	Icon: Of(theme.Label2), Clear: Of(theme.Label2), ClearHover: Of(theme.Label), ClearPressed: Of(theme.Label),
}

// SearchField builds a search field editing buf: a capsule TextField with a
// magnifying glass and, while it holds text, a clear button; Escape clears it
// too (and does not also close a popup or dialog around it).
//
//	if rexui.SearchField(k, "Filter", &v.filter, "Search", rexui.FieldOpts{}).Changed {
//		v.refilter()
//	}
func SearchField(k *base.Kit, label string, buf *ui.TextBuffer, placeholder string, o FieldOpts) base.FieldSignal {
	return field(k, label, buf, placeholder, o, true)
}

// searchDecor builds the magnifying glass and the clear button of the search
// field ti (h pixels high, the clear button's left edge clearW from its end) and
// applies Escape and the clear button's click to res.
func searchDecor(k *base.Kit, ti *base.TextInput, buf *ui.TextBuffer, h, clearW float32, res *base.FieldSignal) {
	c, r := k.Context(), k.Theme()
	rc := &SearchLook
	wkey := ti.WrapKey
	if f, disp := k.Icons().Prepare(c, icon.Search, rc.Icon.RGBA(r)); f != 0 {
		iw, ih := k.IconSize(icon.Search)
		c.NextPos(theme.Snap(k.Pt(rc.IconX)-iw/2), theme.Snap((h-ih)/2))
		c.BoxKeyed(f|base.Floating, ui.KeyN(wkey, 3), disp)
	}
	if res.Cancelled && buf.Len() > 0 {
		k.ClearField(buf, res)
		res.Cancelled = false
	}
	if buf.Len() == 0 {
		return
	}
	cw, ch := k.IconSize(icon.ClearField)
	key := ui.KeyN(wkey, 4)
	hot, down := c.HotTOf(key), c.ActiveTOf(key)
	col := mix(mix(rc.Clear.RGBA(r), rc.ClearHover.RGBA(r), hot), rc.ClearPressed.RGBA(r), down)
	flags := ui.Clickable | ui.HotAnimation | ui.ActiveAnimation | base.Floating
	if ti.Disabled {
		flags = base.Floating
		col = rc.Clear.RGBA(r)
	}
	x := k.FieldWidth(ti) - clearW
	theme.At(c, theme.Snap(x), theme.Snap((h-ch)/2), cw, ch)
	c.NextRole(ui.RoleButton)
	c.NextSemName(str.Clear)
	cb := c.BoxKeyed(flags, key, nil)
	c.Push(cb)
	if f, disp := k.Icons().Prepare(c, icon.ClearField, col); f != 0 {
		c.BoxKeyed(f, ui.KeyN(key, 1), disp)
	}
	c.Pop()
	if c.Signal(cb).Clicked {
		k.ClearField(buf, res)
		c.SetFocus(ti.Key)
		c.SetFocusVisible(false)
	}
}
