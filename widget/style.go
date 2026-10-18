package widget

import (
	"unsafe"

	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// fl is the flag set of a box placed by NextPos on both axes.
const fl = theme.Floating

// Fill is the size of a box that takes the room its siblings leave (a
// fraction of 1 of the parent, giving way to fixed siblings).
var Fill = ui.Pct(1)

// Share is a fraction f of the parent's room, giving way to fixed siblings.
func Share(f float32) ui.Size { return ui.Pct(f) }

// Fixed is a size of pt points.
func (k *Kit) Fixed(pt float32) ui.Size { return ui.Px(theme.Snap(pt * k.scale)) }

// NextSize sets the size of the next widget or container; a size left as the
// zero ui.Size takes the widget's own default.
func (k *Kit) NextSize(w, h ui.Size) { k.NextWidth(w); k.NextHeight(h) }

// NextWidth sets the width of the next widget or container.
func (k *Kit) NextWidth(w ui.Size) { k.nw, k.hasW = w, true }

// NextHeight sets the height of the next widget or container.
func (k *Kit) NextHeight(h ui.Size) { k.nh, k.hasH = h, true }

// NextAlign sets the horizontal alignment of the next label or button text.
func (k *Kit) NextAlign(a ui.Align) { k.nextAlign, k.hasAlign = a, true }

// size gives the next box its size: the widget's defaults, replaced by what
// the app set with NextSize on an axis it set and a size that is not auto.
func (k *Kit) size(defW, defH ui.Size) {
	if k.hasW && k.nw.Kind != ui.SizeAuto {
		defW = k.nw
	}
	if k.hasH && k.nh.Kind != ui.SizeAuto {
		defH = k.nh
	}
	k.hasW, k.hasH = false, false
	k.c.NextSize(defW, defH)
}

// align is the alignment for the next text, def unless the app set one.
func (k *Kit) align(def ui.Align) ui.Align {
	if k.hasAlign {
		k.hasAlign = false
		return k.nextAlign
	}
	return def
}

// vis is the visual state of a widget: how hovered, pressed and focused it is,
// and how strongly its focus ring shows.
type vis struct {
	hot, press, focus float32
	ring              float32
}

// vis reads the animation values of the box with the given key as the previous
// frame left them, or the forced ones (Force). A disabled widget has none.
func (k *Kit) vis(key ui.Key) vis {
	c := k.c
	var v vis
	f := k.force
	k.force = 0
	switch {
	case k.off():
		return v
	case f != 0:
		if f&LookHover != 0 {
			v.hot = 1
		}
		if f&LookPressed != 0 {
			v.press = 1
		}
		if f&LookFocused != 0 {
			v.focus, v.ring = 1, 1
		}
		return v
	}
	v.hot, v.press, v.focus = c.HotTOf(key), c.ActiveTOf(key), c.FocusTOf(key)
	if c.FocusVisible() {
		v.ring = v.focus
	}
	return v
}

// flags are the interaction flags of a widget root: it is clickable and a tab
// stop, animates hover, press and focus, and is inert when disabled.
func (k *Kit) flags() ui.Flags {
	f := ui.Clickable | ui.Focusable | ui.HotAnimation | ui.ActiveAnimation | ui.FocusAnimation
	if k.off() {
		f = ui.Disabled
	}
	return f
}

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
func (k *Kit) decorate(id ui.ID, v vis, radius float32) {
	if v.hot <= 0.004 && v.press <= 0.004 && v.ring <= 0.004 {
		return
	}
	c := k.c
	r := c.Rect(id)
	w, h := r[2]-r[0], r[3]-r[1]
	if w <= 0 || h <= 0 {
		return
	}
	key := c.KeyOf(id)
	if v.hot > 0.004 {
		theme.At(c, 0, 0, w, h)
		c.NextBG(theme.WithAlpha(k.r.Pal.HoverOverlay, v.hot))
		c.NextRadius(radius)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 9001), nil)
	}
	if v.press > 0.004 {
		theme.At(c, 0, 0, w, h)
		c.NextBG(theme.WithAlpha(k.r.Pal.PressOverlay, v.press))
		c.NextRadius(radius)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 9002), nil)
	}
	if v.ring > 0.004 {
		k.ringBox(key, w, h, radius, v.ring)
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
func (k *Kit) font(s TextStyle) text.FontKey {
	switch s {
	case TextSemi, TextAccent:
		return k.r.Font.UISemi
	case TextSmall:
		return k.r.Font.Small
	case TextMono:
		return k.r.Font.Mono
	}
	return k.r.Font.UI
}

// color is the text colour of a text style.
func (k *Kit) color(s TextStyle) uint32 {
	p := &k.r.Pal
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

// lineH is the height of one line of font f in pixels.
func (k *Kit) lineH(f text.FontKey) float32 { return k.r.Met(f).H }

// strBytes views s as bytes without copying.
func strBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

// textW is the advance of s in font f.
func (k *Kit) textW(f text.FontKey, s string) float32 { return k.textWB(f, strBytes(s)) }

// textWB is textW for bytes.
func (k *Kit) textWB(f text.FontKey, b []byte) float32 {
	if len(b) == 0 {
		return 0
	}
	return k.ts.Run(k.ts.Shape(f, b)).Advance
}

// center places the next box with its top at (rowH−h)/2 in a row of height
// rowH, leaving its x to the flow.
func (k *Kit) center(h, rowH float32) {
	k.c.NextPos(0, theme.Snap((rowH-h)/2))
}

// intersects reports whether the rectangle r (x0, y0, x1, y1) shows in the
// window.
func (k *Kit) visible(r [4]float32) bool {
	w, h := k.c.Size()
	return r[2] > r[0] && r[3] > r[1] && r[2] > 0 && r[3] > 0 && r[0] < float32(w) && r[1] < float32(h)
}
