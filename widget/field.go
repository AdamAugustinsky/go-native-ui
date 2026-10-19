package widget

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// FieldSignal is what a text field reports (base.FieldSignal).
type FieldSignal = base.FieldSignal

// FieldKey is the key of the box that takes the keyboard focus in the field
// that TextField or SearchField builds with label inside the box whose key is
// parent. An app moves the focus there with ui.Context.SetFocus before the
// field has been built.
func FieldKey(parent ui.Key, label string) ui.Key { return base.FieldKey(parent, label) }

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

// field is the classic look over base's TextInput primitive.
func (k *Kit) field(label string, buf *ui.TextBuffer, placeholder string, search bool) FieldSignal {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	dim := k.dim()
	ti := k.BeginTextInput(label, theme.Snap(px.ControlH), hoverOn)
	h := ti.H
	font := k.r.Font.UI
	if ti.Mono {
		font = k.r.Font.Mono
	}

	focusT, hot := ti.Focus, ti.Hot
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
	c.NextFont(font)
	c.NextTextColor(p.Text)
	role := ui.RoleTextField
	if search {
		role = ui.RoleSearchField
	}
	// a search field's Escape clears it, and is not also the popup's or dialog's
	res := k.TextInput(&ti, buf, role, search)

	if search {
		k.searchDecor(&ti, buf, h, padL-px.GapSmall, clearW, &res)
	}
	if buf.Len() == 0 && placeholder != "" && !k.Composing(res) {
		theme.At(c, padL, 0, 0, h)
		c.NextSize(ui.TextSize(0), ui.Px(h))
		c.NextFont(font)
		c.NextTextColor(p.TextMuted)
		c.BoxKeyed(ui.DrawText|fl, ui.KeyN(ti.WrapKey, 2), strBytes(placeholder))
	}
	k.EndTextInput(&ti)
	k.undim(dim)
	return res
}

// searchDecor builds the magnifying glass and the clear button of a search
// field, and applies Escape and the clear click.
func (k *Kit) searchDecor(ti *base.TextInput, buf *ui.TextBuffer, h, iconX, clearW float32, res *FieldSignal) {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	wkey := ti.WrapKey
	f, disp := k.icons.Prepare(c, icon.Search, p.TextMuted)
	_, ih := k.icons.Size(icon.Search)
	c.NextPos(px.Pad, theme.Snap((h-ih)/2))
	c.BoxKeyed(f|fl, ui.KeyN(wkey, 3), disp)

	if res.Cancelled && buf.Len() > 0 {
		k.ClearField(buf, res)
		res.Cancelled = false
	}
	if buf.Len() == 0 {
		return
	}
	w := theme.Snap(px.IconSize)
	x := k.FieldWidth(ti) - clearW + (clearW-w)/2 - px.GapSmall/2
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
		k.ClearField(buf, res)
		c.SetFocus(ti.Key)
		c.SetFocusVisible(false)
	}
}
