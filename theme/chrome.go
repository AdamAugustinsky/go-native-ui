package theme

import "gonativeui/ui"

// Floating makes a box a floating child of its container, placed by
// ui.Context.NextPos on both axes.
const Floating = ui.FloatingX | ui.FloatingY

// At positions and sizes the next box (pixels, in the parent's coordinates).
func At(c *ui.Context, x, y, w, h float32) {
	c.NextPos(x, y)
	c.NextSize(ui.Px(w), ui.Px(h))
}

// KeyGen hands out distinct keys for the boxes of one container.
type KeyGen struct {
	// Base is the key the generated keys derive from.
	Base ui.Key
	n    uint64
}

// Next returns the next key.
func (g *KeyGen) Next() ui.Key {
	g.n++
	return ui.KeyN(g.Base, g.n)
}

// Chrome is the window-level drawing a theme owns. It is called a few times
// per frame (never per row), with the Resolved theme of the window it was
// made for. One Chrome serves one window: it keeps the caches of what it
// draws (meshes, gradients) and rebuilds them without allocating when the
// window size, the scale or the light/dark mix changes.
//
// Every method builds boxes in the current parent of c, as floating children
// placed in pixels. Descriptions are passed by value: a pointer handed to an
// interface method escapes to the heap, and a frame must not allocate.
type Chrome interface {
	// Background builds the window background, behind everything else.
	Background(c *ui.Context, r *Resolved)

	// SurfaceShadow builds the shadow of s. Build the shadows of all the
	// surfaces first, so that each surface covers its neighbours' shadows.
	SurfaceShadow(c *ui.Context, r *Resolved, s Surface)
	// BeginSurface builds the surface s and makes it the parent of c. It
	// returns the key generator of the surface's content: build the content
	// with its keys, then call EndSurface with the same s and the generator.
	BeginSurface(c *ui.Context, r *Resolved, s Surface) KeyGen
	// EndSurface builds the surface's edges and closes what BeginSurface
	// opened. kg continues the numbering of the keys BeginSurface returned.
	EndSurface(c *ui.Context, r *Resolved, s Surface, kg KeyGen)

	// Divider builds the grab area of the gutter gut between two surfaces
	// that together fill rect, and the handle it shows while hovered or
	// dragged. It returns the grab box, whose signal the caller reads to move
	// the divider; the box carries the resize cursor for axis.
	Divider(c *ui.Context, r *Resolved, key ui.Key, axis ui.Axis, rect, gut [4]float32) ui.ID

	// Titlebar builds the titlebar band: a window-drag region under whatever
	// else the titlebar holds, the title, and the window buttons asked for.
	Titlebar(c *ui.Context, r *Resolved, t Titlebar) TitlebarResult
}

// Surface describes one surface (a pane, a card) for Chrome. The caller sets
// Focus and Opacity every frame.
type Surface struct {
	// Key is the key of the surface's box, a clipping, clickable box that
	// takes focus presses.
	Key ui.Key
	// Slot indexes the chrome's cache of surface fills. A surface keeps its
	// slot across frames; two surfaces drawn in one frame need two slots. The
	// cache grows the first time a slot is used.
	Slot int
	// Rect is the surface's rectangle in pixels: x0, y0, x1, y1.
	Rect [4]float32
	// Focus is how focused the surface is: 1 is fully focused (drawn
	// opaque), 0 not at all (drawn as translucent glass over the window
	// background); values between are mid-animation.
	Focus float32
	// Opacity multiplies the surface's alpha, 0..1; 1 is normal.
	Opacity float32
	// Top says the surface touches the top of the work area, under the
	// titlebar.
	Top bool
}

// ButtonStyle selects how a titlebar draws window buttons.
type ButtonStyle uint8

const (
	// ButtonsNone draws no window buttons: the system draws them (macOS).
	ButtonsNone ButtonStyle = iota
	// ButtonsTraffic draws the three macOS traffic lights at the left, for
	// offscreen renders. They are decoration and take no input.
	ButtonsTraffic
	// ButtonsCaption draws close, maximise (restore when Maximised) and
	// minimise buttons at the right, for windows that draw their own
	// titlebar (Windows, Wayland client-side decorations, some X11 setups).
	ButtonsCaption
)

// WindowButton names a window button.
type WindowButton uint8

const (
	// ButtonNone is no button.
	ButtonNone WindowButton = iota
	// ButtonClose closes the window.
	ButtonClose
	// ButtonMinimise minimises it.
	ButtonMinimise
	// ButtonMaximise maximises the window, or restores it when it is maximised.
	ButtonMaximise
)

// Glyph names a symbol on a window button.
type Glyph uint8

const (
	// GlyphClose is a cross.
	GlyphClose Glyph = iota
	// GlyphMinimise is a short horizontal bar.
	GlyphMinimise
	// GlyphMaximise is a square.
	GlyphMaximise
	// GlyphRestore is two overlapping squares.
	GlyphRestore
)

// Glyphs supplies the symbols of the window buttons, normally from the icon
// atlas (the widget layer adapts it). A nil Glyphs, or a false return, makes
// the chrome set a text glyph in the theme's symbol font instead.
type Glyphs interface {
	// Glyph builds a floating box showing g centred at (cx, cy) in the parent's
	// coordinates, in colour (RGBA), no larger than size pixels square, and
	// reports whether it could.
	Glyph(c *ui.Context, key ui.Key, g Glyph, cx, cy, size float32, color uint32) bool
}

// Titlebar describes the titlebar of a window for Chrome.
type Titlebar struct {
	// Width is the window width in pixels.
	Width float32
	// Title is shown centred in the free part of the band; empty shows none.
	Title string
	// Buttons selects the window buttons.
	Buttons ButtonStyle
	// Maximised makes the maximise button a restore button.
	Maximised bool
	// Active says the window has focus; an inactive window's title is muted.
	Active bool
	// Glyphs supplies the symbols for ButtonsCaption; may be nil.
	Glyphs Glyphs
}

// TitlebarResult is what a titlebar reports.
type TitlebarResult struct {
	// Clicked is the window button that was clicked this frame, if any.
	Clicked WindowButton
	// Free is the horizontal extent in pixels, x0 and x1, of the band that the
	// window buttons leave to the app (tabs, toolbars).
	Free [2]float32
}
