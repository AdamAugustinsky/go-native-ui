package widget

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
)

// FieldSignal is what a text field reports.
type FieldSignal struct {
	Signal
	// Submitted: Enter went down in the field.
	Submitted bool
	// Cancelled: Escape went down in the field (a search field clears itself
	// then).
	Cancelled bool
}

// TextField builds a single-line text field editing buf, styled by the theme,
// with the placeholder shown while it is empty. It is ui.Context.TextField
// (the caret, selection, mouse, copy, cut, paste, undo and input method are
// documented there) inside the theme's control: the control fill and outline,
// an accent outline and ring while it has the focus, the muted placeholder,
// and the dimmed look when disabled. The width fills the parent unless
// NextWidth says otherwise.
func (k *Kit) TextField(label string, buf *ui.TextBuffer, placeholder string) FieldSignal {
	return k.field(label, buf, placeholder, false)
}

// SearchField is a TextField with a magnifying glass at its start and a clear
// button at its end while it has text; Escape clears it too.
func (k *Kit) SearchField(label string, buf *ui.TextBuffer, placeholder string) FieldSignal {
	return k.field(label, buf, placeholder, true)
}

func (k *Kit) field(label string, buf *ui.TextBuffer, placeholder string, search bool) FieldSignal {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	display, ident := ui.SplitLabel(label)
	dim := k.dim()
	h := theme.Snap(px.ControlH)
	wkey := ui.HashString(c.ParentKey(), ident)
	fkey := ui.HashString(wkey, ident)

	k.size(ui.Pct(1), ui.Px(h))
	wrap := c.BoxKeyed(0, wkey, nil)
	c.Push(wrap)

	focusT := c.FocusTOf(fkey)
	if k.off() {
		focusT = 0
	}
	if k.force&LookFocused != 0 {
		focusT = 1
	}
	hot := c.HotTOf(fkey)
	if k.force&LookHover != 0 {
		hot = 1
	}
	k.force = 0
	border := theme.MixRGBA(p.ControlBorder, p.Accent, focusT)
	bw := theme.Lerp(px.BorderW, px.FocusRingW, focusT)
	bg := p.ControlFill
	if hot > 0.004 && focusT < 1 {
		bg = theme.MixRGBA(bg, theme.MixRGBA(bg, p.HoverOverlay, 0.5), hot)
	}
	padL := px.Pad
	if search {
		_, ih := k.icons.Size(icon.Search)
		padL += ih + px.GapSmall
	}
	padR := px.Pad
	clearW := float32(0)
	if search {
		clearW = theme.Snap(px.IconSize + px.GapSmall)
		if buf.Len() > 0 {
			padR += clearW
		}
	}
	c.NextSize(ui.Pct(1), ui.Px(h))
	c.NextBG(bg)
	c.NextBorder(border, bw)
	c.NextRadius(px.RadiusMedium)
	c.NextPadding(padL, 0, padR, 0)
	c.NextFont(k.r.Font.UI)
	c.NextTextColor(p.Text)
	if k.off() {
		c.NextFlags(ui.Disabled)
	}
	role := ui.RoleTextField
	if search {
		role = ui.RoleSearchField
	}
	c.NextRole(role)
	c.NextSemName(display)
	fs := c.TextField(ident, buf)
	res := FieldSignal{
		Signal:    Signal{ID: fs.ID, Hovering: fs.Hovering, Focused: fs.Focused, Changed: fs.Changed, Clicked: fs.Clicked},
		Submitted: fs.Submitted, Cancelled: fs.Cancelled,
	}

	if search {
		k.searchDecor(wkey, fkey, buf, h, padL-px.GapSmall, clearW, &res)
	}
	if buf.Len() == 0 && placeholder != "" && !k.composing(fs) {
		theme.At(c, padL, 0, 0, h)
		c.NextSize(ui.TextSize(0), ui.Px(h))
		c.NextFont(k.r.Font.UI)
		c.NextTextColor(p.TextMuted)
		c.BoxKeyed(ui.DrawText|fl, ui.KeyN(wkey, 2), strBytes(placeholder))
	}
	c.Pop()
	k.undim(dim)
	return res
}

// composing reports whether the field shows an input-method composition, which
// takes the place of the placeholder.
func (k *Kit) composing(fs ui.TextFieldSignal) bool {
	return fs.Focused && k.c.Input().NPreedit > 0
}

// searchDecor builds the magnifying glass and the clear button of a search
// field, and applies Escape and the clear click.
func (k *Kit) searchDecor(wkey, fkey ui.Key, buf *ui.TextBuffer, h, iconX, clearW float32, res *FieldSignal) {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	f, disp := k.icons.Prepare(c, icon.Search, p.TextMuted)
	_, ih := k.icons.Size(icon.Search)
	c.NextPos(px.Pad, theme.Snap((h-ih)/2))
	c.BoxKeyed(f|fl, ui.KeyN(wkey, 3), disp)

	if res.Cancelled && buf.Len() > 0 {
		buf.Reset()
		res.Changed, res.Cancelled = true, false
	}
	if buf.Len() == 0 {
		return
	}
	w := theme.Snap(px.IconSize)
	x := k.fieldWidth(wkey) - clearW + (clearW-w)/2 - px.GapSmall/2
	theme.At(c, x, theme.Snap((h-w)/2), w, w)
	flags := ui.Clickable | ui.HotAnimation | fl
	if k.off() {
		flags = fl
	}
	key := ui.KeyN(wkey, 4)
	hot := c.HotTOf(key)
	c.NextBG(theme.WithAlpha(p.HoverOverlay, hot))
	c.NextRadius(w / 2)
	c.NextRole(ui.RoleButton)
	c.NextSemName("Clear")
	cb := c.BoxKeyed(flags|ui.DrawBackground, key, nil)
	c.Push(cb)
	cf, cd := k.icons.Prepare(c, icon.Close, theme.MixRGBA(p.TextMuted, p.Text, hot))
	c.NextPos(0, 0)
	c.NextSize(ui.Px(w), ui.Px(w))
	c.BoxKeyed(cf|fl, ui.KeyN(key, 1), cd)
	c.Pop()
	if c.Signal(cb).Clicked {
		buf.Reset()
		res.Changed = true
		c.SetFocus(fkey)
		c.SetFocusVisible(false)
	}
}

// fieldWidth is the width in pixels the field had in the previous frame.
func (k *Kit) fieldWidth(wkey ui.Key) float32 {
	r := k.c.RectOf(wkey)
	return r[2] - r[0]
}
