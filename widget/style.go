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

// ringBox builds a focus ring of w×h pixels at the origin of the current
// parent.
func (k *Kit) ringBox(key ui.Key, w, h, radius, t float32) {
	c := k.c
	theme.At(c, 0, 0, w, h)
	c.NextBorder(theme.WithAlpha(k.r.Pal.FocusRing, t), k.r.Px.FocusRingW)
	c.NextRadius(radius)
	c.BoxKeyed(ui.DrawBorder|fl, ui.KeyN(key, 9003), nil)
}

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
