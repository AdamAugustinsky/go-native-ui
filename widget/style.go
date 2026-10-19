package widget

import (
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// fl is the flag set of a box placed by NextPos on both axes.
const fl = theme.Floating

// Fill is the size of a box that takes the room its siblings leave (a
// fraction of 1 of the parent, giving way to fixed siblings).
var Fill = ui.Pct(1)

// Share is a fraction f of the parent's room, giving way to fixed siblings.
func Share(f float32) ui.Size { return ui.Pct(f) }

// The classic look reacts to hover on every control.
const hoverOn = base.HoverOn

// size gives the next box its size: the widget's defaults, replaced by what
// the app set with NextSize on an axis it set and a size that is not auto.
func (k *Kit) size(defW, defH ui.Size) { k.ApplySize(defW, defH) }

// align is the alignment for the next text, def unless the app set one.
func (k *Kit) align(def ui.Align) ui.Align { return k.TakeAlign(def) }

// dim starts the dimming of a disabled widget; pair it with undim.
func (k *Kit) dim() bool {
	if k.off() {
		k.c.PushOpacity(disabledOpacity)
		return true
	}
	return false
}

func (k *Kit) undim(on bool) {
	if on {
		k.c.PopOpacity()
	}
}

// decorate builds the hover and press tints and the focus ring over the widget
// root id, which must be the current parent. They are sized by the root's
// rectangle of the previous frame, so a widget whose size just changed
// decorates the old size for one frame.
func (k *Kit) decorate(id ui.ID, v base.Visual, radius float32) {
	if v.Hot <= 0.004 && v.Down <= 0.004 && v.Ring <= 0.004 {
		return
	}
	c := k.c
	r := c.Rect(id)
	w, h := r[2]-r[0], r[3]-r[1]
	if w <= 0 || h <= 0 {
		return
	}
	k.decorateBox(c.KeyOf(id), w, h, v, radius)
}

// decorateBox builds the hover and press tints and the focus ring of a control
// whose visible box is the current parent, w×h pixels. The classic look draws
// the ring inside the box.
func (k *Kit) decorateBox(key ui.Key, w, h float32, v base.Visual, radius float32) {
	c := k.c
	if v.Hot > 0.004 {
		theme.At(c, 0, 0, w, h)
		c.NextBG(theme.WithAlpha(k.r.Pal.HoverOverlay, v.Hot))
		c.NextRadius(radius)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 9001), nil)
	}
	if v.Down > 0.004 {
		theme.At(c, 0, 0, w, h)
		c.NextBG(theme.WithAlpha(k.r.Pal.PressOverlay, v.Down))
		c.NextRadius(radius)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 9002), nil)
	}
	if v.Ring > 0.004 {
		k.ringBox(key, w, h, radius, v.Ring)
	}
}

// ringBox builds a focus ring of w×h pixels at the origin of the current
// parent.
func (k *Kit) ringBox(key ui.Key, w, h, radius, t float32) {
	c := k.c
	theme.At(c, 0, 0, w, h)
	c.NextBorder(theme.WithAlpha(k.r.Pal.FocusRing, t), k.r.Px.FocusRingW)
	c.NextRadius(radius)
	c.BoxKeyed(ui.DrawBorder|fl, ui.KeyN(key, 9003), nil)
}

// font is the font of a text style.
func (k *Kit) font(s TextStyle) text.FontKey { return fontOf(k.r, s) }

// color is the text colour of a text style.
func (k *Kit) color(s TextStyle) uint32 { return colorOf(k.r, s) }

// fontOf is the font of a text style in the resolved theme r.
func fontOf(r *theme.Resolved, s TextStyle) text.FontKey {
	switch s {
	case TextSemi, TextAccent:
		return r.Font.UISemi
	case TextSmall:
		return r.Font.Small
	case TextMono:
		return r.Font.Mono
	}
	return r.Font.UI
}

// colorOf is the text colour of a text style in the resolved theme r.
func colorOf(r *theme.Resolved, s TextStyle) uint32 {
	p := &r.Pal
	switch s {
	case TextMuted, TextSmall:
		return p.TextMuted
	case TextAccent:
		return p.Accent
	case TextDanger:
		return p.Danger
	}
	return p.Text
}

// strBytes views s as bytes without copying.
func strBytes(s string) []byte { return base.StrBytes(s) }

// textW is the advance of s in font f.
func (k *Kit) textW(f text.FontKey, s string) float32 { return k.TextWidth(f, s) }

// textWB is textW for bytes.
func (k *Kit) textWB(f text.FontKey, b []byte) float32 { return k.TextWidthBytes(f, b) }

// center places the next box with its top at (rowH−h)/2 in a row of height
// rowH, leaving its x to the flow.
func (k *Kit) center(h, rowH float32) { k.CenterNext(h, rowH) }
