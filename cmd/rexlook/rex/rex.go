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
// The scene follows the system's light or dark mode and accent colour (D32): the
// first frame is drawn in the system's mode, a later change crossfades, and the
// mode is declared to the window with ui.Context.SetChromeDark, so that the
// parts the OS draws follow it. SetPreference forces light or dark instead.
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
	"gonativeui/icon"
	"gonativeui/text"
	"gonativeui/theme"
	rextheme "gonativeui/theme/rex"
	"gonativeui/ui"
)

// WindowPt is the window size of the reference in points; TitlebarPt the
// height of the custom titlebar (the panes start below it). The window
// system draws the traffic lights at x = 21 pt, y = 22 pt.
const (
	WindowWidthPt  = 1057
	WindowHeightPt = 639
	TitlebarPt     = 46 // the Rex theme's TitlebarH (TestTitlebarAndIconsMatchTheme)
)

// TextSystem is what the scene needs from the text system: shaping through
// ui.Shaper plus creating fonts. *text.System satisfies it.
type TextSystem interface {
	ui.Shaper
	Font(d text.FontDesc) text.FontKey
}

var _ TextSystem = (*text.System)(nil)

type (
	keyGen  = theme.KeyGen
	palette = rextheme.Terminal
)

// Rex is the state of the scene: the tabs and panes, animations that are not
// stored in the ui context, and the fonts and icons.
type Rex struct {
	// Traffic makes the scene draw the window's traffic lights itself (for
	// offscreen output; in a window the system draws them).
	Traffic bool

	ts    TextSystem
	Scale float32 // pixels per point
	K     float32 // pixels per reference pixel (the reference is 2×)

	// The Rex theme resolved for this window (fonts, metrics, palette and the
	// chrome that draws the background, panes, dividers and titlebar), the
	// monospace cell grid of the terminal content, and the icons.
	th    theme.Resolved
	cells rextheme.Grid
	icons *icon.Atlas
	fol   theme.Follower // light or dark, and the accent: the system's unless a preference says otherwise

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
	r.icons = icon.New(ts, iconSpecs[:]...)
	r.SetScale(scale)
	r.initTabs()
}

// AttachRenderer gives the icon atlas a texture on u (the renderer) and
// uploads it. Call it before the first frame and again when the renderer is
// recreated; symbol icons draw nothing until it has been called.
func (r *Rex) AttachRenderer(u ui.Uploader) { r.icons.Attach(u) }

// SetPreference sets which mode the scene shows: the system's (the default),
// or light or dark whatever the system does. A change crossfades.
func (r *Rex) SetPreference(p theme.Preference) { r.fol.SetPreference(p) }

// Preference is the preference set with SetPreference.
func (r *Rex) Preference() theme.Preference { return r.fol.Preference() }

func (r *Rex) uid() uint32 {
	r.nextUID++
	return r.nextUID
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
	if s := c.Scale(); s != r.Scale {
		r.SetScale(s)
	}
	r.fol.Update(c, &r.th)
	if w != r.lastW || h != r.lastH {
		r.glide = false // a resize follows the window exactly
		r.lastW, r.lastH = w, h
	}

	r.update(c)
	r.draw(c)
}
