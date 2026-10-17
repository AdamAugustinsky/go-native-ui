// Package rex is the interactive Rex look (docs/design/rex-benchmark.md)
// built on the immediate-mode ui package: a window background, a titlebar
// with a tab capsule and host block, and panes of terminal-like text that
// can be focused, split, maximised, closed and resized.
//
// It is OS-independent: it draws through ui.Context and the text system it is
// given and renders the same on every backend. Only app.go, the glue to package
// app, knows about the window loop; the scene never touches the platform.
// The window loop owns the frame (see package app):
//
//	x := rex.New(ts, scale)       // fonts and icons at the backing scale
//	x.AttachRenderer(renderer)    // icon atlas texture (and again if the renderer is recreated)
//	each frame: c.Begin(in, w, h, scale, dt); x.Build(c); c.End(&list)
//	after the frame: hand c.CursorRegions, c.ActiveCursor and c.DragRegions to the window
//
// Package app does all of that for a *Rex built with NewApp (it implements
// app.Builder and app.Initer, see app.go); Config is its window.
//
// Lengths are written in the reference screenshot's pixels (2×) and scaled by
// c.Scale()/2, so the look is exact at scale 2 and scales elsewhere.
//
// A change of c.Scale() between frames recreates the fonts and re-rasterises
// the icons (allocating, once). Everything else, including a change of the
// window size (live resize), rebuilds from the model without allocating.
//
// New and Build (when the scale changed) rasterise the system symbols, which
// on macOS goes through AppKit: call them on the thread that runs the window
// loop, like the text system.
package rex

import (
	"math"
	"unsafe"

	"gonativeui/text"
	"gonativeui/ui"
)

// WindowPt is the window size of the reference in points; TitlebarPt the
// height of the custom titlebar (the panes start below it). The window
// system draws the traffic lights at x = 21 pt, y = 22 pt.
const (
	WindowWidthPt  = 1057
	WindowHeightPt = 639
	TitlebarPt     = 46
)

// TextSystem is what the scene needs from the text system: shaping through
// ui.Shaper plus creating fonts. *text.System satisfies it.
type TextSystem interface {
	ui.Shaper
	Font(d text.FontDesc) text.FontKey
}

var _ TextSystem = (*text.System)(nil)

// Rex is the state of the scene: the tabs and panes, animations that are not
// stored in the ui context, and the fonts and icons.
type Rex struct {
	// Traffic makes the scene draw the window's traffic lights itself (for
	// offscreen output; in a window the system draws them).
	Traffic bool

	ts    TextSystem
	scale float32
	k     float32 // pixels per reference pixel

	fUI, fUISemi, fSub, fSym, fMono, fMonoBold text.FontKey
	fm                                         []fontMet
	adv, rowPitch                              float32 // terminal cell, pixels
	monoFace                                   string
	monoSizePt                                 float32

	ic     *icons
	fld    field
	meshes [maxPanes]mesh

	tabs    [maxTabs]tab
	nTabs   int
	active  int
	nextUID uint32
	switchN uint32 // counts tab changes: restarts the content fade-in

	glide  bool // structural change in progress: panes glide to their places
	moving bool // some pane is still gliding

	lastW, lastH int
	disp         [maxPanes][4]float32 // rectangles the panes were drawn at

	// geometry of the latest frame
	capsule  [4]float32
	tabRects [maxTabs][4]float32
	pill     [4]float32
}

type fontMet struct{ h, off float32 }

// New creates the scene for a window at scale pixels per point. ts must stay
// valid for the life of the Rex and is used from the thread that builds
// frames.
func New(ts TextSystem, scale float32) *Rex {
	r := NewApp()
	r.init(ts, scale)
	return r
}

// NewApp returns a Rex that the window loop (package app) finishes in Init,
// once the text system and the renderer exist; set Traffic before that if the
// scene draws the traffic lights itself.
func NewApp() *Rex { return &Rex{nextUID: 1} }

func (r *Rex) init(ts TextSystem, scale float32) {
	r.ts = ts
	r.ic = newIcons()
	r.setScale(scale)
	r.initTabs()
}

// AttachRenderer gives the icon atlas a texture on u (the renderer) and
// uploads it. Call it before the first frame and again when the renderer is
// recreated; icons draw as text glyphs until it has been called.
func (r *Rex) AttachRenderer(u ui.Uploader) { r.ic.attach(u) }

// CellAdvancePt, RowPitchPt and MonoFace describe the terminal grid in use.
func (r *Rex) CellAdvancePt() float64 { return float64(r.adv) / float64(r.scale) }
func (r *Rex) RowPitchPt() float64    { return float64(r.rowPitch) / float64(r.scale) }
func (r *Rex) MonoFace() string       { return r.monoFace }
func (r *Rex) MonoSizePt() float64    { return float64(r.monoSizePt) }

func (r *Rex) uid() uint32 {
	r.nextUID++
	return r.nextUID
}

// setScale (re)creates everything that depends on the backing scale.
func (r *Rex) setScale(s float32) {
	r.scale, r.k = s, s/2
	r.setupFonts()
	r.ic.raster(s)
	for i := range r.meshes {
		r.meshes[i].valid = false
	}
	r.fld.w = 0
}

func strBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

func (r *Rex) width(f text.FontKey, s string) float32 {
	return r.ts.Run(r.ts.Shape(f, strBytes(s))).Advance
}

func (r *Rex) setupFonts() {
	ts, s := r.ts, r.scale
	r.fUI = ts.Font(text.FontDesc{SizePt: 14, Weight: 400, Scale: s})
	r.fUISemi = ts.Font(text.FontDesc{SizePt: 12.75, Weight: 600, Scale: s})
	r.fSub = ts.Font(text.FontDesc{SizePt: 10.8, Weight: 400, Scale: s})
	r.fSym = ts.Font(text.FontDesc{SizePt: 12.5, Weight: 400, Scale: s})

	// Monospace: a system face sized so that the cell advance is 7.5 pt, the
	// pitch measured on the reference (see rex-regions.json). The first
	// family that really is monospaced wins (Menlo on macOS, Consolas on
	// Windows, ...); otherwise the system monospaced design is used.
	var probe text.FontKey
	family := ""
	found := false
	for _, fam := range [...]string{"Menlo", "Consolas", "DejaVu Sans Mono", "Liberation Mono"} {
		p := ts.Font(text.FontDesc{Family: fam, SizePt: 12.5, Weight: 400, Scale: s})
		narrow, wide := r.width(p, "i"), r.width(p, "m")
		if math.Abs(float64(narrow-wide)) <= 0.01*float64(wide) {
			family, probe, found = fam, p, true
			break
		}
	}
	if !found {
		probe = ts.Font(text.FontDesc{Mono: true, SizePt: 12.5, Weight: 400, Scale: s})
	}
	r.monoFace = family
	if family == "" {
		r.monoFace = "system monospaced"
	}
	adv := ts.Metrics(probe).MonoAdvance
	if adv <= 0 {
		adv = r.width(probe, "0")
	}
	if adv <= 0 {
		adv = 7.5 * s
	}
	size := 12.5 * (7.5 * s) / adv
	r.monoSizePt = size
	mk := func(weight int) text.FontKey {
		return ts.Font(text.FontDesc{Family: family, Mono: family == "", SizePt: size, Weight: weight, Scale: s})
	}
	r.fMono, r.fMonoBold = mk(400), mk(700)
	r.adv = ts.Metrics(r.fMono).MonoAdvance
	if r.adv <= 0 {
		r.adv = r.width(r.fMono, "0")
	}
	r.rowPitch = 16 * s

	// Text boxes are one whole-pixel line high; off places the baseline in
	// them the way ui does, so a label can be put at a baseline position.
	maxKey := 0
	for _, f := range [...]text.FontKey{r.fUI, r.fUISemi, r.fSub, r.fSym, r.fMono, r.fMonoBold} {
		maxKey = max(maxKey, int(f))
	}
	r.fm = make([]fontMet, maxKey+1)
	for _, f := range [...]text.FontKey{r.fUI, r.fUISemi, r.fSub, r.fSym, r.fMono, r.fMonoBold} {
		m := ts.Metrics(f)
		lh := m.LineHeight
		if lh <= 0 {
			lh = m.Ascent + m.Descent + m.LineGap
		}
		h := float32(math.Ceil(float64(max(lh, m.Ascent+m.Descent))))
		r.fm[f] = fontMet{h: h, off: snap((h-(m.Ascent+m.Descent))/2 + m.Ascent)}
	}
}

// keyGen hands out distinct keys for the boxes of one container.
type keyGen struct {
	base ui.Key
	n    uint64
}

func (g *keyGen) next() ui.Key {
	g.n++
	return ui.KeyN(g.base, g.n)
}

var (
	keyRoot  = ui.HashString(0, "rex")
	keyTab   = ui.HashString(keyRoot, "tab")
	keyPane  = ui.HashString(keyRoot, "pane")
	keyDiv   = ui.HashString(keyRoot, "divider")
	keyPlus  = ui.HashString(keyRoot, "plus")
	keyCmd   = ui.HashString(keyRoot, "cmd")
	keyBG    = ui.HashString(keyRoot, "bg")
	keyBar   = ui.HashString(keyRoot, "titlebar")
	keyAnim  = ui.HashString(keyRoot, "anim")
	keyFade  = ui.HashString(keyAnim, "fade")
	keyFocus = ui.HashString(keyAnim, "focus")
	keyMaxT  = ui.HashString(keyAnim, "max")
	keyAnyT  = ui.HashString(keyAnim, "maxany")
	keyTabT  = ui.HashString(keyAnim, "tab")
	keyGlide = ui.HashString(keyAnim, "glide")
)

func tabKey(uid uint32) ui.Key  { return ui.KeyN(keyTab, uint64(uid)) }
func paneKey(uid uint32) ui.Key { return ui.KeyN(keyPane, uint64(uid)) }
func divKey(uid uint32) ui.Key  { return ui.KeyN(keyDiv, uint64(uid)) }

// buttons of a pane header, left to right.
const (
	btnSplitX = iota
	btnSplitY
	btnMax
	btnClose
	numBtns
)

func btnKey(paneUID uint32, b int) ui.Key { return ui.KeyN(paneKey(paneUID), uint64(b)+1) }

// Build builds the frame: it applies the input of the frame to the model
// (clicks, drags), then builds every box. Call it between ui.Begin and ui.End.
func (r *Rex) Build(c *ui.Context) {
	w, h := c.Size()
	if w <= 0 || h <= 0 || c.Scale() <= 0 {
		return // minimised or not yet laid out: nothing to draw
	}
	if s := c.Scale(); s != r.scale {
		r.setScale(s)
	}
	if w != r.lastW || h != r.lastH {
		r.glide = false // a resize follows the window exactly
		r.lastW, r.lastH = w, h
	}
	r.fld.build(w, h, r.k)

	r.update(c)
	r.draw(c)
}
