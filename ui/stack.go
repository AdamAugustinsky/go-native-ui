package ui

import (
	"gonativeui/gpu"
	"gonativeui/text"
)

const maxStack = 32

// stack is a style stack with a one-shot "next" value. v[0] is the default;
// the box builder takes the next value if one was set (and clears it),
// otherwise the top of the stack.
type stack[T any] struct {
	v       [maxStack]T
	n       int
	next    T
	hasNext bool
	cur     *T    // the top's mirror in Context.cur
	pending *bool // Context.hasNextStyle
}

func (s *stack[T]) init(def T) { s.v[0], s.n, s.hasNext = def, 0, false }

func (s *stack[T]) bind(cur *T, pending *bool) {
	s.cur, s.pending = cur, pending
	*cur = s.v[s.n]
}

func (s *stack[T]) reset() { s.n, s.hasNext = 0, false; *s.cur = s.v[0] }

func (s *stack[T]) top() T { return s.v[s.n] }

func (s *stack[T]) push(x T) {
	if s.n+1 >= maxStack {
		panic("ui: style stack overflow")
	}
	s.n++
	s.v[s.n] = x
	*s.cur = x
}

func (s *stack[T]) pop() {
	if s.n == 0 {
		panic("ui: style stack underflow")
	}
	s.n--
	*s.cur = s.v[s.n]
}

// peek is the value the next box would take, without taking it.
func (s *stack[T]) peek() T {
	if s.hasNext {
		return s.next
	}
	return s.v[s.n]
}

func (s *stack[T]) set(x T) { s.next, s.hasNext = x, true; *s.pending = true }

// takeInto stores the next value in *dst, if one was set, and consumes it.
func (s *stack[T]) takeInto(dst *T) {
	if s.hasNext {
		*dst, s.hasNext = s.next, false
	}
}

type border struct {
	Color uint32
	Width float32
}

// The style setters come in three forms per property: Next… applies to the
// next box built only, Push… applies to every box built until the matching
// Pop…, and the builder falls back to the default (see New) below the
// bottom of the stack. Like RAD's stacks, a pushed value is NOT inherited by
// a box's children through the tree: it is simply the current value for
// every box built while it is on the stack, descendants included.

// Background: a single colour or one colour per corner (TL, TR, BL, BR),
// RGBA8 0xRRGGBBAA. Needs DrawBackground.

// NextBG sets the background colour col (RGBA8) of the next box only.
func (c *Context) NextBG(col uint32) { c.sBG.set([4]uint32{col, col, col, col}) }

// NextBG4 sets the next box's background colours for its corners TL, TR, BL, BR.
func (c *Context) NextBG4(tl, tr, bl, br uint32) { c.sBG.set([4]uint32{tl, tr, bl, br}) }

// PushBG pushes background colour col for the boxes built until PopBG.
func (c *Context) PushBG(col uint32) { c.sBG.push([4]uint32{col, col, col, col}) }

// PushBG4 pushes per-corner background colours (TL, TR, BL, BR) until PopBG.
func (c *Context) PushBG4(tl, tr, bl, br uint32) { c.sBG.push([4]uint32{tl, tr, bl, br}) }

// PopBG pops the background pushed by PushBG or PushBG4.
func (c *Context) PopBG() { c.sBG.pop() }

// Text colour. Needs DrawText.

// NextTextColor sets the text colour col (RGBA8) of the next box only.
func (c *Context) NextTextColor(col uint32) { c.sText.set(col) }

// PushTextColor pushes text colour col for the boxes built until PopTextColor.
func (c *Context) PushTextColor(col uint32) { c.sText.push(col) }

// PopTextColor pops the text colour pushed by PushTextColor.
func (c *Context) PopTextColor() { c.sText.pop() }

// Border colour and width. Needs DrawBorder.

// NextBorder sets the border colour (RGBA8) and width (pixels) of the next box only.
func (c *Context) NextBorder(col uint32, width float32) { c.sBorder.set(border{col, width}) }

// PushBorder pushes border colour col and width (pixels) until PopBorder.
func (c *Context) PushBorder(col uint32, width float32) { c.sBorder.push(border{col, width}) }

// PopBorder pops the border pushed by PushBorder.
func (c *Context) PopBorder() { c.sBorder.pop() }

// Border sides: which edges DrawBorder outlines (default SidesAll).

// NextBorderSides sets the edges DrawBorder outlines on the next box only.
func (c *Context) NextBorderSides(s Sides) { c.sSides.set(s) }

// PushBorderSides pushes the edges DrawBorder outlines until PopBorderSides.
func (c *Context) PushBorderSides(s Sides) { c.sSides.push(s) }

// PopBorderSides pops the sides pushed by PushBorderSides.
func (c *Context) PopBorderSides() { c.sSides.pop() }

// Pointer cursor shown over the box (default CursorArrow, which marks nothing);
// see Context.CursorRegions.

// NextCursor sets the pointer cursor shown over the next box only.
func (c *Context) NextCursor(cur Cursor) { c.sCursor.set(cur) }

// PushCursor pushes the pointer cursor shown over the boxes built until PopCursor.
func (c *Context) PushCursor(cur Cursor) { c.sCursor.push(cur) }

// PopCursor pops the cursor pushed by PushCursor.
func (c *Context) PopCursor() { c.sCursor.pop() }

// Image drawn by DrawImage: the texel rectangle (u0, v0, u1, v1) of texture t
// in the given mode.

// NextImage sets the image DrawImage draws on the next box only.
func (c *Context) NextImage(t gpu.TextureID, uv [4]float32, mode gpu.Mode) {
	c.sImage.set(Image{Tex: t, UV: uv, Mode: mode})
}

// PushImage pushes the image DrawImage draws until PopImage.
func (c *Context) PushImage(t gpu.TextureID, uv [4]float32, mode gpu.Mode) {
	c.sImage.push(Image{Tex: t, UV: uv, Mode: mode})
}

// NextImageFit sets the premultiplied colour image DrawImage draws on the
// next box only, placed in the box by fit with the box's corner radii (a
// photo, an avatar; see ImageFit).
func (c *Context) NextImageFit(t gpu.TextureID, uv [4]float32, fit ImageFit) {
	c.sImage.set(Image{Tex: t, UV: uv, Mode: gpu.ModeRGBA, Fit: fit})
}

// PopImage pops the image pushed by PushImage.
func (c *Context) PopImage() { c.sImage.pop() }

// NextPath makes the next box's image (DrawImage) path p filled, through m
// (gpu.PathMask, which keeps the texture while p stays the same), in the
// box's text colour, at the path's own size and centred in the box: a box
// as large as the path (its furthest point) shows it at the box's top left.
// It returns the path's size in pixels; with no uploader yet (Uploader) it
// sets nothing and returns zeros.
func (c *Context) NextPath(m *gpu.PathMask, p *gpu.Path) (w, h float32) {
	u := c.Uploader()
	if u == nil {
		return 0, 0
	}
	tex, tw, th := m.Mask(u, p)
	if tex == 0 {
		return 0, 0
	}
	c.NextImage(tex, [4]float32{0, 0, float32(tw), float32(th)}, gpu.ModeMaskR8)
	return float32(tw), float32(th)
}

// Corner radii (TL, TR, BR, BL), pixels.

// NextRadius sets the same corner radius r (pixels) on all corners of the next box.
func (c *Context) NextRadius(r float32) { c.sRadii.set([4]float32{r, r, r, r}) }

// NextFadeEnd makes the next box's children fade out over its last w pixels
// on the right, to nothing at its end: a strip that scrolls sideways fades
// under whatever is behind it (glass included), which a gradient drawn over
// it in a solid colour cannot do. It applies to what the children draw, not
// to the box's own background.
func (c *Context) NextFadeEnd(w float32) { c.nextFade = max(w, 0) }

// SetFadeEnd is NextFadeEnd for box id, built this frame (a scroll view's
// viewport, which the widget built).
func (c *Context) SetFadeEnd(id ID, w float32) {
	if id > 0 && int(id) < len(c.boxes) {
		c.boxes[id].fadeEnd = max(w, 0)
	}
}

// NextRadii sets the corner radii (pixels) of the next box: top-left, top-right, bottom-right, bottom-left.
func (c *Context) NextRadii(tl, tr, br, bl float32) { c.sRadii.set([4]float32{tl, tr, br, bl}) }

// PushRadius pushes the same corner radius r (pixels) on all corners until PopRadii.
func (c *Context) PushRadius(r float32) { c.sRadii.push([4]float32{r, r, r, r}) }

// PushRadii pushes corner radii (pixels) TL, TR, BR, BL until PopRadii.
func (c *Context) PushRadii(tl, tr, br, bl float32) { c.sRadii.push([4]float32{tl, tr, br, bl}) }

// PopRadii pops the radii pushed by PushRadius or PushRadii.
func (c *Context) PopRadii() { c.sRadii.pop() }

// Shadows: up to two layers, drawn first layer first. Needs DrawShadow.

// NextShadow sets one shadow layer s on the next box only.
func (c *Context) NextShadow(s Shadow) { c.sShadow.set([2]Shadow{s}) }

// NextShadows sets two shadow layers on the next box only; a is drawn first.
func (c *Context) NextShadows(a, b Shadow) { c.sShadow.set([2]Shadow{a, b}) }

// PushShadow pushes one shadow layer s until PopShadows.
func (c *Context) PushShadow(s Shadow) { c.sShadow.push([2]Shadow{s}) }

// PushShadows pushes two shadow layers, a drawn first, until PopShadows.
func (c *Context) PushShadows(a, b Shadow) { c.sShadow.push([2]Shadow{a, b}) }

// PopShadows pops the shadows pushed by PushShadow or PushShadows.
func (c *Context) PopShadows() { c.sShadow.pop() }

// Font of the display text (a text.FontKey created through the text system).

// NextFont sets the display-text font of the next box only.
func (c *Context) NextFont(f text.FontKey) { c.sFont.set(f) }

// PushFont pushes the display-text font for the boxes built until PopFont.
func (c *Context) PushFont(f text.FontKey) { c.sFont.push(f) }

// PopFont pops the font pushed by PushFont.
func (c *Context) PopFont() { c.sFont.pop() }

// Horizontal text alignment.

// NextAlign sets the horizontal alignment of the next box's own display text
// (DrawText) only. It does not move the box's children: on a box that draws no
// text it does nothing. To place children, use NextJustify (along the layout
// axis) and NextCross (across it): a capsule that centres its icon is
// NextJustify(JustifyCenter) with NextCross(CrossCenter). Context.Audit
// reports a NextAlign on a box that has children and no text.
func (c *Context) NextAlign(a Align) { c.sAlign.set(a) }

// PushAlign pushes the horizontal text alignment until PopAlign. Like
// NextAlign it applies to the display text of the boxes built meanwhile, not to
// their children.
func (c *Context) PushAlign(a Align) { c.sAlign.push(a) }

// PopAlign pops the alignment pushed by PushAlign.
func (c *Context) PopAlign() { c.sAlign.pop() }

// Extra flags for the next box built: they are added to the flags its builder
// gives it (Disabled on a TextField, for one).

// NextFlags adds the flags f to the next box built.
func (c *Context) NextFlags(f Flags) { c.nextFlags |= f }

// Padding (left, top, right, bottom): the inset of the content area in which
// children are laid out and text is drawn.

// NextPadding sets the padding (left, top, right, bottom, pixels) of the next box only.
func (c *Context) NextPadding(l, t, r, b float32) { c.sPad.set([4]float32{l, t, r, b}) }

// NextPad sets the same padding (pixels) on all four sides of the next box.
func (c *Context) NextPad(all float32) { c.sPad.set([4]float32{all, all, all, all}) }

// PushPadding pushes padding (left, top, right, bottom, pixels) until PopPadding.
func (c *Context) PushPadding(l, t, r, b float32) { c.sPad.push([4]float32{l, t, r, b}) }

// PushPad pushes the same padding (pixels) on all four sides until PopPadding.
func (c *Context) PushPad(all float32) { c.sPad.push([4]float32{all, all, all, all}) }

// PopPadding pops the padding pushed by PushPadding or PushPad.
func (c *Context) PopPadding() { c.sPad.pop() }

// Child gap: space between children along the layout axis.

// NextGap sets the gap (pixels) between the next box's children.
func (c *Context) NextGap(g float32) { c.sGap.set(g) }

// PushGap pushes the child gap (pixels) until PopGap.
func (c *Context) PushGap(g float32) { c.sGap.push(g) }

// PopGap pops the gap pushed by PushGap.
func (c *Context) PopGap() { c.sGap.pop() }

// Child layout axis.

// NextAxis sets the child layout axis of the next box only.
func (c *Context) NextAxis(a Axis) { c.sAxis.set(a) }

// PushAxis pushes the child layout axis until PopAxis.
func (c *Context) PushAxis(a Axis) { c.sAxis.push(a) }

// PopAxis pops the axis pushed by PushAxis.
func (c *Context) PopAxis() { c.sAxis.pop() }

// Cross-axis placement of the children.

// NextCross sets where the next box places its flow children across its layout
// axis (CrossStart by default): the heights of the children of a row, the
// widths of the children of a column. A child larger than the room (a box
// that overflows its parent) stays at the start.
func (c *Context) NextCross(a Cross) { c.sCross.set(a) }

// PushCross pushes the cross-axis placement until PopCross.
func (c *Context) PushCross(a Cross) { c.sCross.push(a) }

// PopCross pops the placement pushed by PushCross.
func (c *Context) PopCross() { c.sCross.pop() }

// Main-axis placement of the children.

// NextJustify sets where the next box places its flow children along its
// layout axis, in the free space its padded area has left (JustifyStart by
// default). See Justify for how it meets percentages, overflow, scrolling and
// flows.
func (c *Context) NextJustify(j Justify) { c.sJustify.set(j) }

// PushJustify pushes the main-axis placement until PopJustify.
func (c *Context) PushJustify(j Justify) { c.sJustify.push(j) }

// PopJustify pops the placement pushed by PushJustify.
func (c *Context) PopJustify() { c.sJustify.pop() }

// Preferred sizes per axis.

// NextWidth sets the preferred width of the next box only.
func (c *Context) NextWidth(s Size) { c.sW.set(s) }

// NextHeight sets the preferred height of the next box only.
func (c *Context) NextHeight(s Size) { c.sH.set(s) }

// NextSize sets the preferred width and height of the next box only.
func (c *Context) NextSize(w, h Size) { c.sW.set(w); c.sH.set(h) }

// PushWidth pushes the preferred width until PopWidth.
func (c *Context) PushWidth(s Size) { c.sW.push(s) }

// PushHeight pushes the preferred height until PopHeight.
func (c *Context) PushHeight(s Size) { c.sH.push(s) }

// PushSize pushes the preferred width and height until PopSize.
func (c *Context) PushSize(w, h Size) { c.sW.push(w); c.sH.push(h) }

// PopWidth pops the width pushed by PushWidth.
func (c *Context) PopWidth() { c.sW.pop() }

// PopHeight pops the height pushed by PushHeight.
func (c *Context) PopHeight() { c.sH.pop() }

// PopSize pops the width and height pushed by PushSize.
func (c *Context) PopSize() { c.sW.pop(); c.sH.pop() }

// Opacity (0..1) multiplies the alpha of every colour of the box. Push
// composes with the opacity already on the stack, so nested pushes multiply.

// NextOpacity multiplies the next box's opacity by o (0..1) on top of the current one.
func (c *Context) NextOpacity(o float32) { c.sOpacity.set(c.sOpacity.top() * o) }

// PushOpacity pushes opacity o (0..1) times the current one until PopOpacity.
func (c *Context) PushOpacity(o float32) { c.sOpacity.push(c.sOpacity.top() * o) }

// PopOpacity pops the opacity pushed by PushOpacity.
func (c *Context) PopOpacity() { c.sOpacity.pop() }

// Palette transform: move every colour toward col by t. Push it around a
// subtree to dim it (focus dimming).

// NextTint moves every colour of the next box toward col by t (0..1).
func (c *Context) NextTint(col uint32, t float32) { c.sTint.set(Tint{col, t}) }

// PushTint moves every colour of the boxes built until PopTint toward col by t (0..1).
func (c *Context) PushTint(col uint32, t float32) { c.sTint.push(Tint{col, t}) }

// PopTint pops the tint pushed by PushTint.
func (c *Context) PopTint() { c.sTint.pop() }

// NextPos sets the fixed position (x, y) of the next box in pixels relative
// to its parent's top-left corner, used on the axes where the box is
// FloatingX/FloatingY. Children of the root are positioned in window
// coordinates.
func (c *Context) NextPos(x, y float32) { c.nextPos, c.hasNextPos = [2]float32{x, y}, true }
