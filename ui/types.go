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

import (
	"gonativeui/gpu"
	"gonativeui/text"
)

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

	// DrawImage draws the box's Image (see NextImage) centred in its padded
	// rectangle at the image's own size, tinted by the text colour.
	DrawImage

	// WindowDrag marks an area that moves the window when it is pressed and
	// dragged (a custom titlebar) and that the window's double-click action
	// applies to. Boxes that take input and are drawn above it keep their
	// clicks; see Context.DragRegions. The box itself takes no input.
	WindowDrag

	// Disabled takes the box out of interaction: it is not hit-tested, cannot
	// take focus (Tab skips it, and it loses the focus it had), and its Signal
	// reports nothing, also for an activation or press that was already
	// queued. Its drawing is unchanged: a widget dims itself. The flag
	// applies to this box only; set it on each box that should be inert.
	Disabled

	// WrapText breaks the box's display text into lines at the box's width
	// (CoreText's typesetter on macOS, the line break opportunities of UAX #14
	// elsewhere; always at newline characters) and draws it line by line. A
	// box whose height is TextSize is as high as the lines. A width left on
	// auto is Pct(1). See Context.Label, and RichText for styled text.
	WrapText

	// KeepFocus makes a press on the box (or on a descendant that takes none)
	// leave the keyboard focus where it is, as the menu bar of an operating
	// system does: a click on it does not take the focus from the text field
	// it acts on. The box can still take the focus from the keyboard (Tab,
	// SetFocus).
	KeepFocus

	// NoFontSmoothing draws the box's text without the operating system's
	// font smoothing: on macOS, glyph stems keep their outline weight instead
	// of the thickening AppKit gives labels (D38). For terminal-like content,
	// as Ghostty draws by default; UI text keeps the default.
	NoFontSmoothing

	// EllipsisOverflowX ends one line of display text that is wider than the
	// box with "…" in its font, cut after the last glyph that leaves room for
	// it (spaces before the cut are dropped), as AppKit truncates a label's
	// tail (NSLineBreakByTruncatingTail). Right-to-left text fades out
	// instead (FadeOverflowX). Rich and wrapped text use text.Para's
	// Ellipsis.
	EllipsisOverflowX
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

// Cross is where a box places its flow children across its layout axis: at
// the start (the top of a row, the left of a column), centred, or at the end.
type Cross uint8

const (
	// CrossStart places the children at the start of the padded area (the
	// default).
	CrossStart Cross = iota
	// CrossCenter centres each child across the padded area.
	CrossCenter
	// CrossEnd places each child at the end of the padded area.
	CrossEnd
)

// Justify is where a linear box (AxisX or AxisY) places its flow children
// along its layout axis, in the room its padded area has left once the
// children are sized and the gaps between them counted (the free space):
//
//   - Start and End are the leading and trailing edge of the axis. ui's linear
//     boxes have no text direction: a row runs left to right and a column top
//     to bottom, so Start is the left of a row and the top of a column (a
//     mirrored row is built in reverse order). The one layout that has a
//     direction, the flow, has its own alignment, FlowOpts.Align, whose
//     FlowStart, FlowCenter and FlowEnd follow FlowOpts.RTL; a flow box
//     ignores Justify.
//   - Only the free space moves: children keep their sizes and the gap stays
//     the least space between two of them. A child sized Pct(1), or children
//     whose percentages add up to the whole, leave no free space, and neither
//     does a box sized by its children (the default); Justify then changes
//     nothing. Percentages below the whole leave the rest, which is placed like
//     any other free space.
//   - Children that do not fit (no free space, or less than none) are placed
//     from Start, as without Justify: content never moves past the leading
//     edge.
//   - A scrolling box (ScrollX, ScrollY) justifies its content along the
//     scrolled axis while the content is smaller than the box, and scrolls it
//     from Start once it is larger. A virtual list (ListRange) counts its
//     whole extent, not only the rows built.
//   - Floating children take no part: they keep their fixed position.
//
// Justify is the main-axis counterpart of Cross; Align is about a box's own
// text and does not move children.
type Justify uint8

const (
	// JustifyStart places the children from the leading edge (the default).
	JustifyStart Justify = iota
	// JustifyCenter centres the children as a group, the free space split
	// evenly before the first and after the last (the leading part rounded to
	// a whole pixel, as CrossCenter does).
	JustifyCenter
	// JustifyEnd places the children against the trailing edge.
	JustifyEnd
	// JustifySpaceBetween puts the first child at the leading edge, the last at
	// the trailing edge and shares the free space between the gaps; a single
	// child stays at the leading edge.
	JustifySpaceBetween
	// JustifySpaceEvenly shares the free space equally between the edges and
	// the gaps: the same extra space before the first child, between every two
	// and after the last.
	JustifySpaceEvenly
)

// Align positions a box's text horizontally inside its padded rectangle. It is
// about the box's own display text only: it does not move the box's children
// (see Justify and Cross for those).
type Align uint8

const (
	// AlignLeft starts the text at the left of the padded rectangle.
	AlignLeft Align = iota
	// AlignCenter centres the text.
	AlignCenter
	// AlignRight ends the text at the right of the padded rectangle.
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
	// Kind selects how Value is read.
	Kind SizeKind
	// Value is the size in pixels, a fraction or extra pixels, according to Kind.
	Value float32
	// Strictness (0..1) is how unwilling the size is to shrink when the parent is too small.
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
	// OffX and OffY are the shadow's offset from the box, in pixels.
	OffX, OffY float32
	// Sigma is the gaussian blur sigma in pixels; 0 disables the layer.
	Sigma float32
	Color uint32 // RGBA8, 0xRRGGBBAA
}

// Sides selects the edges of a box outline that DrawBorder draws. A corner
// belongs to the sides that touch it: with SideLeft|SideRight the outline runs
// down both sides and round all four corners (the arcs up to where the top and
// bottom edges turn straight), and the flat top and bottom edges stay bare.
type Sides uint8

const (
	// SideLeft is the left edge.
	SideLeft Sides = 1 << iota
	// SideTop is the top edge.
	SideTop
	// SideRight is the right edge.
	SideRight
	// SideBottom is the bottom edge.
	SideBottom
	// SidesAll, the default, draws the whole outline in one quad.
	SidesAll = SideLeft | SideTop | SideRight | SideBottom
)

// Image is a rectangle of a texture drawn by DrawImage: UV is the texel
// rectangle (u0, v0, u1, v1), which is also the size in pixels it is drawn at.
// Mode is gpu.ModeMaskR8 for coverage masks such as symbols (tinted by the
// box's text colour) or gpu.ModeRGBA for premultiplied colour images (drawn
// with the alpha of the text colour).
type Image struct {
	// Tex is the texture to draw.
	Tex gpu.TextureID
	// UV is the texel rectangle u0, v0, u1, v1.
	UV [4]float32
	// Mode is how the texture is read (gpu.ModeMaskR8 or gpu.ModeRGBA).
	Mode gpu.Mode
	// Fit places the image in the box (see ImageFit); FitNatural draws it at
	// its own size.
	Fit ImageFit
}

// Tint is the palette transform: every colour of a box (background, border,
// text, shadows, hover overlay) is moved toward Color's RGB by T (0 = none,
// 1 = Color), alpha unchanged. Push it around a subtree to dim it.
type Tint struct {
	// Color is the colour (RGBA8; its alpha is ignored) the box's colours move toward.
	Color uint32
	// T is how far they move: 0 not at all, 1 all the way.
	T float32
}

// Theme holds the colours of the built-in hover/press overlay.
type Theme struct {
	HotOverlay    uint32 // RGBA8; alpha is multiplied by hot_t
	ActiveOverlay uint32 // RGBA8; alpha is multiplied by active_t
}

// Rates are the animation rates k in rate = 1−2^(−k·dt), per second.
type Rates struct {
	// Hot is the rate of the hover animation (HotT).
	Hot float32
	// Active is the rate of the press animation (ActiveT).
	Active float32
	// Focus is the rate of the focus animation (FocusT).
	Focus float32
	// Scroll is the rate at which a scroll offset approaches its target.
	Scroll float32
}

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
