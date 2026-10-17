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
}

func (s *stack[T]) init(def T) { s.v[0], s.n, s.hasNext = def, 0, false }

func (s *stack[T]) reset() { s.n, s.hasNext = 0, false }

func (s *stack[T]) top() T { return s.v[s.n] }

func (s *stack[T]) push(x T) {
	if s.n+1 >= maxStack {
		panic("ui: style stack overflow")
	}
	s.n++
	s.v[s.n] = x
}

func (s *stack[T]) pop() {
	if s.n == 0 {
		panic("ui: style stack underflow")
	}
	s.n--
}

func (s *stack[T]) set(x T) { s.next, s.hasNext = x, true }

func (s *stack[T]) take() T {
	if s.hasNext {
		s.hasNext = false
		return s.next
	}
	return s.v[s.n]
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

func (c *Context) NextBG(col uint32)             { c.sBG.set([4]uint32{col, col, col, col}) }
func (c *Context) NextBG4(tl, tr, bl, br uint32) { c.sBG.set([4]uint32{tl, tr, bl, br}) }
func (c *Context) PushBG(col uint32)             { c.sBG.push([4]uint32{col, col, col, col}) }
func (c *Context) PushBG4(tl, tr, bl, br uint32) { c.sBG.push([4]uint32{tl, tr, bl, br}) }
func (c *Context) PopBG()                        { c.sBG.pop() }

// Text colour. Needs DrawText.

func (c *Context) NextTextColor(col uint32) { c.sText.set(col) }
func (c *Context) PushTextColor(col uint32) { c.sText.push(col) }
func (c *Context) PopTextColor()            { c.sText.pop() }

// Border colour and width. Needs DrawBorder.

func (c *Context) NextBorder(col uint32, width float32) { c.sBorder.set(border{col, width}) }
func (c *Context) PushBorder(col uint32, width float32) { c.sBorder.push(border{col, width}) }
func (c *Context) PopBorder()                           { c.sBorder.pop() }

// Border sides: which edges DrawBorder outlines (default SidesAll).

func (c *Context) NextBorderSides(s Sides) { c.sSides.set(s) }
func (c *Context) PushBorderSides(s Sides) { c.sSides.push(s) }
func (c *Context) PopBorderSides()         { c.sSides.pop() }

// Pointer cursor shown over the box (default CursorArrow, which marks nothing);
// see Context.CursorRegions.

func (c *Context) NextCursor(cur Cursor) { c.sCursor.set(cur) }
func (c *Context) PushCursor(cur Cursor) { c.sCursor.push(cur) }
func (c *Context) PopCursor()            { c.sCursor.pop() }

// Image drawn by DrawImage: the texel rectangle (u0, v0, u1, v1) of texture t
// in the given mode.

func (c *Context) NextImage(t gpu.TextureID, uv [4]float32, mode gpu.Mode) {
	c.sImage.set(Image{t, uv, mode})
}
func (c *Context) PushImage(t gpu.TextureID, uv [4]float32, mode gpu.Mode) {
	c.sImage.push(Image{t, uv, mode})
}
func (c *Context) PopImage() { c.sImage.pop() }

// Corner radii (TL, TR, BR, BL), pixels.

func (c *Context) NextRadius(r float32)             { c.sRadii.set([4]float32{r, r, r, r}) }
func (c *Context) NextRadii(tl, tr, br, bl float32) { c.sRadii.set([4]float32{tl, tr, br, bl}) }
func (c *Context) PushRadius(r float32)             { c.sRadii.push([4]float32{r, r, r, r}) }
func (c *Context) PushRadii(tl, tr, br, bl float32) { c.sRadii.push([4]float32{tl, tr, br, bl}) }
func (c *Context) PopRadii()                        { c.sRadii.pop() }

// Shadows: up to two layers, drawn first layer first. Needs DrawShadow.

func (c *Context) NextShadow(s Shadow)     { c.sShadow.set([2]Shadow{s}) }
func (c *Context) NextShadows(a, b Shadow) { c.sShadow.set([2]Shadow{a, b}) }
func (c *Context) PushShadow(s Shadow)     { c.sShadow.push([2]Shadow{s}) }
func (c *Context) PushShadows(a, b Shadow) { c.sShadow.push([2]Shadow{a, b}) }
func (c *Context) PopShadows()             { c.sShadow.pop() }

// Font of the display text (a text.FontKey created through the text system).

func (c *Context) NextFont(f text.FontKey) { c.sFont.set(f) }
func (c *Context) PushFont(f text.FontKey) { c.sFont.push(f) }
func (c *Context) PopFont()                { c.sFont.pop() }

// Horizontal text alignment.

func (c *Context) NextAlign(a Align) { c.sAlign.set(a) }
func (c *Context) PushAlign(a Align) { c.sAlign.push(a) }
func (c *Context) PopAlign()         { c.sAlign.pop() }

// Padding (left, top, right, bottom): the inset of the content area in which
// children are laid out and text is drawn.

func (c *Context) NextPadding(l, t, r, b float32) { c.sPad.set([4]float32{l, t, r, b}) }
func (c *Context) NextPad(all float32)            { c.sPad.set([4]float32{all, all, all, all}) }
func (c *Context) PushPadding(l, t, r, b float32) { c.sPad.push([4]float32{l, t, r, b}) }
func (c *Context) PushPad(all float32)            { c.sPad.push([4]float32{all, all, all, all}) }
func (c *Context) PopPadding()                    { c.sPad.pop() }

// Child gap: space between children along the layout axis.

func (c *Context) NextGap(g float32) { c.sGap.set(g) }
func (c *Context) PushGap(g float32) { c.sGap.push(g) }
func (c *Context) PopGap()           { c.sGap.pop() }

// Child layout axis.

func (c *Context) NextAxis(a Axis) { c.sAxis.set(a) }
func (c *Context) PushAxis(a Axis) { c.sAxis.push(a) }
func (c *Context) PopAxis()        { c.sAxis.pop() }

// Preferred sizes per axis.

func (c *Context) NextWidth(s Size)   { c.sW.set(s) }
func (c *Context) NextHeight(s Size)  { c.sH.set(s) }
func (c *Context) NextSize(w, h Size) { c.sW.set(w); c.sH.set(h) }
func (c *Context) PushWidth(s Size)   { c.sW.push(s) }
func (c *Context) PushHeight(s Size)  { c.sH.push(s) }
func (c *Context) PushSize(w, h Size) { c.sW.push(w); c.sH.push(h) }
func (c *Context) PopWidth()          { c.sW.pop() }
func (c *Context) PopHeight()         { c.sH.pop() }
func (c *Context) PopSize()           { c.sW.pop(); c.sH.pop() }

// Opacity (0..1) multiplies the alpha of every colour of the box. Push
// composes with the opacity already on the stack, so nested pushes multiply.

func (c *Context) NextOpacity(o float32) { c.sOpacity.set(c.sOpacity.top() * o) }
func (c *Context) PushOpacity(o float32) { c.sOpacity.push(c.sOpacity.top() * o) }
func (c *Context) PopOpacity()           { c.sOpacity.pop() }

// Palette transform: move every colour toward col by t. Push it around a
// subtree to dim it (focus dimming).

func (c *Context) NextTint(col uint32, t float32) { c.sTint.set(Tint{col, t}) }
func (c *Context) PushTint(col uint32, t float32) { c.sTint.push(Tint{col, t}) }
func (c *Context) PopTint()                       { c.sTint.pop() }

// NextPos sets the fixed position (x, y) of the next box in pixels relative
// to its parent's top-left corner, used on the axes where the box is
// FloatingX/FloatingY. Children of the root are positioned in window
// coordinates.
func (c *Context) NextPos(x, y float32) { c.nextPos, c.hasNextPos = [2]float32{x, y}, true }
