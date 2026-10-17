package rexkit

import (
	"math"
	"unsafe"

	"gonativeui/text"
	"gonativeui/ui"
)

// TextSystem is what the kit needs from the text system: shaping through
// ui.Shaper plus creating fonts. *text.System satisfies it.
type TextSystem interface {
	ui.Shaper
	Font(d text.FontDesc) text.FontKey
}

var _ TextSystem = (*text.System)(nil)

const (
	// Fl makes a box a floating child of its container, placed by NextPos.
	Fl = ui.FloatingX | ui.FloatingY
	// TitlebarPt is the height of the custom titlebar in points; the window
	// system draws the traffic lights at x = 21 pt, y = 22 pt.
	TitlebarPt = 46
)

var (
	keyRoot = ui.HashString(0, "rex")
	keyBG   = ui.HashString(keyRoot, "bg")
	keyBar  = ui.HashString(keyRoot, "titlebar")
	keyPane = ui.HashString(keyRoot, "pane")
)

// FontMet places the baseline in a text box of one whole-pixel line: H is the
// box height, Off the baseline's distance from its top.
type FontMet struct{ H, Off float32 }

// Kit is the state shared by every scene in the Rex look: the scale, the
// fonts on the terminal grid, the icon atlas, the background field and the
// pane fill meshes. A scene embeds it.
type Kit struct {
	TS    TextSystem
	Scale float32
	K     float32 // pixels per reference pixel

	FUI, FUISemi, FSub, FSym, FMono, FMonoBold text.FontKey
	FM                                         []FontMet
	Adv, RowPitch                              float32 // terminal cell, pixels
	monoFace                                   string
	monoSizePt                                 float32

	IC     *Icons
	Fld    Field
	Meshes []Mesh
}

// Init sets the kit up for a window at scale pixels per point: fonts, icon
// atlas (specs) and nMeshes pane fill meshes. It rasterises the symbols,
// which on macOS goes through AppKit, so call it on the thread that runs the
// window loop.
func (k *Kit) Init(ts TextSystem, specs []IconSpec, nMeshes int, scale float32) {
	k.TS = ts
	k.IC = NewIcons(specs)
	k.Meshes = make([]Mesh, nMeshes)
	k.SetScale(scale)
}

// SetScale (re)creates everything that depends on the backing scale.
func (k *Kit) SetScale(s float32) {
	k.Scale, k.K = s, s/2
	k.setupFonts()
	k.IC.Raster(s)
	for i := range k.Meshes {
		k.Meshes[i].valid = false
	}
	k.Fld.w = 0
}

// CellAdvancePt, RowPitchPt, MonoFace and MonoSizePt describe the terminal
// grid in use.
func (k *Kit) CellAdvancePt() float64 { return float64(k.Adv) / float64(k.Scale) }
func (k *Kit) RowPitchPt() float64    { return float64(k.RowPitch) / float64(k.Scale) }
func (k *Kit) MonoFace() string       { return k.monoFace }
func (k *Kit) MonoSizePt() float64    { return float64(k.monoSizePt) }

// StrBytes views s as bytes without copying.
func StrBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

// Width is the advance of s in font f.
func (k *Kit) Width(f text.FontKey, s string) float32 {
	return k.TS.Run(k.TS.Shape(f, StrBytes(s))).Advance
}

func (k *Kit) setupFonts() {
	ts, s := k.TS, k.Scale
	k.FUI = ts.Font(text.FontDesc{SizePt: 14, Weight: 400, Scale: s})
	k.FUISemi = ts.Font(text.FontDesc{SizePt: 12.75, Weight: 600, Scale: s})
	k.FSub = ts.Font(text.FontDesc{SizePt: 10.8, Weight: 400, Scale: s})
	k.FSym = ts.Font(text.FontDesc{SizePt: 12.5, Weight: 400, Scale: s})

	// Monospace: a system face sized so that the cell advance is 7.5 pt, the
	// pitch measured on the reference (see rex-regions.json). The first
	// family that really is monospaced wins (Menlo on macOS, Consolas on
	// Windows, ...); otherwise the system monospaced design is used.
	var probe text.FontKey
	family := ""
	found := false
	for _, fam := range [...]string{"Menlo", "Consolas", "DejaVu Sans Mono", "Liberation Mono"} {
		p := ts.Font(text.FontDesc{Family: fam, SizePt: 12.5, Weight: 400, Scale: s})
		narrow, wide := k.Width(p, "i"), k.Width(p, "m")
		if math.Abs(float64(narrow-wide)) <= 0.01*float64(wide) {
			family, probe, found = fam, p, true
			break
		}
	}
	if !found {
		probe = ts.Font(text.FontDesc{Mono: true, SizePt: 12.5, Weight: 400, Scale: s})
	}
	k.monoFace = family
	if family == "" {
		k.monoFace = "system monospaced"
	}
	adv := ts.Metrics(probe).MonoAdvance
	if adv <= 0 {
		adv = k.Width(probe, "0")
	}
	if adv <= 0 {
		adv = 7.5 * s
	}
	size := 12.5 * (7.5 * s) / adv
	k.monoSizePt = float32(size)
	mk := func(weight int) text.FontKey {
		return ts.Font(text.FontDesc{Family: family, Mono: family == "", SizePt: size, Weight: weight, Scale: s})
	}
	k.FMono, k.FMonoBold = mk(400), mk(700)
	k.Adv = ts.Metrics(k.FMono).MonoAdvance
	if k.Adv <= 0 {
		k.Adv = k.Width(k.FMono, "0")
	}
	k.RowPitch = 16 * s

	// Text boxes are one whole-pixel line high; Off places the baseline in
	// them the way ui does, so a label can be put at a baseline position.
	maxKey := 0
	for _, f := range [...]text.FontKey{k.FUI, k.FUISemi, k.FSub, k.FSym, k.FMono, k.FMonoBold} {
		maxKey = max(maxKey, int(f))
	}
	k.FM = make([]FontMet, maxKey+1)
	for _, f := range [...]text.FontKey{k.FUI, k.FUISemi, k.FSub, k.FSym, k.FMono, k.FMonoBold} {
		m := ts.Metrics(f)
		lh := m.LineHeight
		if lh <= 0 {
			lh = m.Ascent + m.Descent + m.LineGap
		}
		h := float32(math.Ceil(float64(max(lh, m.Ascent+m.Descent))))
		k.FM[f] = FontMet{H: h, Off: Snap((h-(m.Ascent+m.Descent))/2 + m.Ascent)}
	}
}

// KeyGen hands out distinct keys for the boxes of one container.
type KeyGen struct {
	Base ui.Key
	n    uint64
}

// Next returns the next key.
func (g *KeyGen) Next() ui.Key {
	g.n++
	return ui.KeyN(g.Base, g.n)
}

// At positions and sizes the next box (pixels, in the parent's coordinates).
func At(c *ui.Context, x, y, w, h float32) {
	c.NextPos(x, y)
	c.NextSize(ui.Px(w), ui.Px(h))
}

// Put places the text s with its pen origin at (x, baseline) in the
// coordinates of the current parent. width > 0 fixes the box width (text
// wider than that fades out, with ui.FadeOverflowX in extra).
func (k *Kit) Put(c *ui.Context, kg *KeyGen, f text.FontKey, s string, x, baseline float32, color uint32, width float32, extra ui.Flags) {
	k.PutBytes(c, kg, f, StrBytes(s), x, baseline, color, width, extra)
}

// PutBytes is Put for a byte slice, which must stay valid until the frame ends.
func (k *Kit) PutBytes(c *ui.Context, kg *KeyGen, f text.FontKey, s []byte, x, baseline float32, color uint32, width float32, extra ui.Flags) {
	fm := k.FM[f]
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
	c.BoxKeyed(ui.DrawText|Fl|extra, kg.Next(), s)
}

// IconAt builds a box that shows icon id centred at (cx, cy) in the parent's
// coordinates, in colour col (RGBA).
func (k *Kit) IconAt(c *ui.Context, kg *KeyGen, id IconID, cx, cy float32, color uint32) {
	flags, disp := k.IC.Set(c, id)
	var w, h float32
	if flags == ui.DrawImage {
		sr := k.IC.rects[id]
		w, h = float32(sr.w), float32(sr.h)
	} else {
		w, h = 20*k.K, k.FM[k.FSym].H
		c.NextFont(k.FSym)
		c.NextAlign(ui.AlignCenter)
	}
	c.NextTextColor(color)
	At(c, Snap(cx-w/2), Snap(cy-h/2), w, h)
	c.BoxKeyed(flags|Fl, kg.Next(), disp)
}

// IconButton builds a square hit box of side hit centred at (cx, cy) with
// icon id centred in it. The icon goes from colour base to hover as the
// pointer comes in, over a faint rounded background. A disabled button is
// drawn faded and takes no input.
func (k *Kit) IconButton(c *ui.Context, key ui.Key, id IconID, cx, cy, hit float32, base, hover uint32, enabled bool) {
	t := c.HotTOf(key)
	pressed := c.ActiveTOf(key)
	flags := ui.DrawBackground | Fl
	bx, by := Snap(cx-hit/2), Snap(cy-hit/2)
	if enabled {
		flags |= ui.Clickable | ui.HotAnimation | ui.ActiveAnimation
		c.NextCursor(ui.CursorPointer)
		c.NextBG(ColA(0x2A2030, 0.08*t+0.07*pressed))
	} else {
		c.PushOpacity(0.4)
	}
	At(c, bx, by, hit, hit)
	c.NextRadius(6 * k.Scale)
	btn := c.BoxKeyed(flags, key, nil)
	if !enabled {
		c.PopOpacity()
	}
	// The icon is placed against the parent's grid, not the button's.
	c.Push(btn)
	kg := KeyGen{Base: ui.KeyN(key, 1000)}
	if !enabled {
		c.PushOpacity(0.4)
	}
	k.IconAt(c, &kg, id, cx-bx, cy-by, Col(MixRGB(base, hover, max(t, pressed))))
	if !enabled {
		c.PopOpacity()
	}
	c.Pop()
}
