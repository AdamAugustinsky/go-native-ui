// Package ui is the immediate-mode UI core, after the RAD Debugger's
// ui_core (github.com/EpicGamesExt/raddebugger, MIT; DECISIONS D10).
//
// The app rebuilds the box tree every frame through a small builder API.
// Boxes are identified by 64-bit keys hashed from the parent key and a label,
// so their state (hover/press animation, scroll offset, previous rectangle)
// survives as long as they keep being built. Hit testing and ListRange use
// the previous frame's layout, so a widget learns it was clicked while it is
// being built.
//
// # Frame
//
//	c.Begin(&in, widthPx, heightPx, scale, dt)  // hit test + input
//	...build boxes with Box / Push / Pop, ask Signal(id)...
//	c.End(&list)                                // layout, animation, prune, emit quads
//	// the app encodes list through the gpu.Renderer
//
// # Units
//
// Every length is in physical pixels (the unit of gpu.Quad and of Input).
// Scale is exposed (Context.Scale, Context.Pt) for apps that think in points.
//
// # Memory
//
// Steady-state frames allocate nothing: the box cache, key table, frame
// strings, animation values and the draw list reuse their storage, and bulk
// data is pointer-free (indices into pools and arenas). Everything grows
// during warm-up only; Reserve pre-sizes the box cache.
//
// # Threading
//
// A Context is single-threaded and is used from the main thread, like the
// text.System it drives.
package ui

import "gonativeui/text"

// Key identifies a box across frames. 0 is the nil key.
type Key uint64

// ID names a box for the current frame: the index of its record in the box
// pool. 0 is "no box". IDs are only meaningful between Begin and End of the
// frame that produced them.
type ID uint32

// Flags select what a box does.
type Flags uint32

const (
	// Interaction.
	Clickable Flags = 1 << iota // press/release/click/drag signals; implies hit testing
	Hoverable                   // takes part in hit testing (hover signal; also blocks boxes beneath it)
	Focusable                   // a press on the box (or a descendant that cannot take it) moves focus here
	ScrollX                     // scrolls horizontally; children are not shrunk on X and may overflow
	ScrollY                     // scrolls vertically; children are not shrunk on Y and may overflow

	// Drawing.
	DrawBackground // fill with the background colours
	DrawBorder     // outline with the border colour and width
	DrawShadow     // draw the shadow layers beneath the box
	DrawText       // draw the display text
	DrawHotEffects // translucent hover/press overlay driven by hot_t and active_t (see Theme)
	Clip           // clip children to the box rectangle

	// Layout.
	FloatingX // x position is the fixed position relative to the parent, outside the flow
	FloatingY // y position is the fixed position relative to the parent, outside the flow

	// Text.
	FadeOverflowX // text wider than the box fades out at the right edge instead of being cut

	// Animation.
	HotAnimation    // animate hot_t toward hover
	ActiveAnimation // animate active_t toward press
	FocusAnimation  // animate focus_t toward focus
)

// scrollable flags, either axis.
const scrollMask = ScrollX | ScrollY

// hitMask are the flags that make a box a hit-test target.
const hitMask = Clickable | Hoverable | Focusable

// floatingMask are the flags that pull a box out of the flow on some axis.
const floatingMask = FloatingX | FloatingY

// Axis is the direction in which a box lays out its children.
type Axis uint8

const (
	AxisX Axis = iota // children placed left to right
	AxisY             // children stacked top to bottom (the default)
)

// Align positions a box's text horizontally inside its padded rectangle.
type Align uint8

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// SizeKind selects how one axis of a box is sized.
type SizeKind uint8

const (
	// SizeAuto, the zero value, resolves per box: the text size when the
	// box draws text, otherwise the sum of its children.
	SizeAuto SizeKind = iota
	// SizePixels is Value pixels.
	SizePixels
	// SizeText is the extent of the display text (advance on X, line height
	// on Y) plus the box's padding on that axis plus Value extra pixels.
	SizeText
	// SizeParentPct is Value (a fraction, 1 = 100 %) of the parent's content
	// area (its size minus its padding) on that axis. A parent that sizes
	// itself from its children counts a percentage child as 0 when it sums.
	SizeParentPct
	// SizeChildren is the sum of the children along the layout axis
	// (including the child gap) or the largest child across it, plus the
	// box's padding. Floating children don't count.
	SizeChildren
)

// Size is the preferred size of a box on one axis. Strictness (0..1) says how
// willing the size is to be kept when the parent is too small: 1 never
// shrinks, 0 gives way entirely, and the overflow is shared between siblings
// in proportion to size×(1−strictness).
type Size struct {
	Kind       SizeKind
	Value      float32
	Strictness float32
}

// Px is a fixed size in pixels (strictness 1).
func Px(v float32) Size { return Size{SizePixels, v, 1} }

// Pct is a fraction f of the parent's content area (strictness 0).
func Pct(f float32) Size { return Size{SizeParentPct, f, 0} }

// TextSize is the text extent plus the box padding plus extraPx (strictness 1).
func TextSize(extraPx float32) Size { return Size{SizeText, extraPx, 1} }

// Children is the extent of the children with the given strictness.
func Children(strictness float32) Size { return Size{SizeChildren, 0, strictness} }

// Strict returns s with strictness f.
func (s Size) Strict(f float32) Size { s.Strictness = f; return s }

// Shadow is one drop-shadow layer: the box's rounded rectangle offset by
// (OffX, OffY), blurred with a gaussian of the given sigma. Sigma 0 disables
// the layer.
type Shadow struct {
	OffX, OffY float32
	Sigma      float32
	Color      uint32 // RGBA8, 0xRRGGBBAA
}

// Tint is the palette transform: every colour of a box (background, border,
// text, shadows, hover overlay) is moved toward Color's RGB by T (0 = none,
// 1 = Color), alpha unchanged. Push it around a subtree to dim it.
type Tint struct {
	Color uint32
	T     float32
}

// Theme holds the colours of the built-in hover/press overlay.
type Theme struct {
	HotOverlay    uint32 // RGBA8; alpha is multiplied by hot_t
	ActiveOverlay uint32 // RGBA8; alpha is multiplied by active_t
}

// Rates are the animation rates k in rate = 1−2^(−k·dt), per second.
type Rates struct{ Hot, Active, Focus, Scroll float32 }

// Shaper is the part of text.System that ui uses; *text.System satisfies it.
// Tests substitute a deterministic fake.
type Shaper interface {
	Metrics(f text.FontKey) text.Metrics
	Shape(f text.FontKey, utf8 []byte) text.RunRef
	Run(r text.RunRef) text.RunView
	RunGeneration() uint32
	Glyph(face text.FaceID, gid uint16, sub uint8) text.GlyphEntry
	AtlasGeneration() uint32
	Pages() int
	PageInfo(p int) (w, h int, color bool)
	Dirty(p int) (x, y, w, h int, pixels []byte, stride int, ok bool)
	ClearDirty(p int)
}

var _ Shaper = (*text.System)(nil)
