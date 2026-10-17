package ui

import (
	"math"

	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/text"
)

const maxDepth = 64

// persist is the part of a box record that survives from frame to frame while
// the box keeps being built, including the previous layout's results (which
// hit testing, ListRange and scroll clamping read during the next build).
type persist struct {
	key       Key
	lastFrame uint32
	hotT      float32
	activeT   float32
	focusT    float32
	scroll    [2]float64 // current offset, pixels
	scrollTgt [2]float64
	rect      [4]float32 // snapped screen rectangle
	size      [2]float32
	origin    [2]float32 // unsnapped top-left
	content   [2]float64 // extent of the children incl. padding (virtual extent for lists)
}

// box is one record of the pointer-free box pool. Everything after persist
// is rebuilt each frame by the builder.
type box struct {
	persist

	flags                           Flags
	parent, first, last, next, prev ID

	bg          [4]uint32
	textColor   uint32
	borderColor uint32
	borderW     float32
	radii       [4]float32
	shadow      [2]Shadow
	tint        Tint
	opacity     float32
	font        text.FontKey
	align       Align
	axis        Axis
	pad         [4]float32 // l, t, r, b
	gap         float32
	pref        [2]Size
	fixed       [2]float32

	txt    core.Span // display text in the current frame's arena
	run    text.RunRef
	runGen uint32
	textW  float32

	listRows  int
	listFirst int
	listRowH  float32
}

type fontInfo struct {
	ok                  bool
	ascent, descent, lh float32
}

// Context is the UI state: the box cache, input/interaction state,
// animation values and the style stacks. Create one with New.
type Context struct {
	// Theme and Rates may be changed by the app before the first frame.
	Theme Theme
	Rates Rates
	// WheelStep is the number of pixels one wheel notch scrolls (default 48).
	WheelStep float32

	sh Shaper

	boxes     []box
	pool      *core.Pool
	table     *core.Table
	order     []ID // boxes built this frame, parents before children
	prevOrder []ID
	root      ID
	frame     uint32
	w, h      int
	scale, dt float32
	in        Input
	fr        core.Frame
	fmtA      core.Arena
	fmt       Fmt

	parents  [maxDepth]ID
	nparents int

	sBG      stack[[4]uint32]
	sText    stack[uint32]
	sBorder  stack[border]
	sRadii   stack[[4]float32]
	sShadow  stack[[2]Shadow]
	sFont    stack[text.FontKey]
	sAlign   stack[Align]
	sPad     stack[[4]float32]
	sGap     stack[float32]
	sAxis    stack[Axis]
	sW, sH   stack[Size]
	sOpacity stack[float32]
	sTint    stack[Tint]

	nextPos    [2]float32
	hasNextPos bool

	// interaction (see signal.go)
	hotKey, scrollKey Key
	hotID             ID
	active            [2]Key
	pressedKey        [2]Key
	releasedKey       [2]Key
	pressPos          [2][2]float32
	pressClicks       [2]uint8
	focusKey          Key
	scrollApplied     [2]float32

	// animation (see anim.go)
	animating             bool
	rHot, rActive, rFocus float32
	rScroll               float32
	av                    []animEnt
	avPool                *core.Pool
	avTable               *core.Table

	fonts []fontInfo

	up      Uploader
	pageTex []gpu.TextureID
	texOK   []bool
}

// New returns a Context drawing text through sh.
func New(sh Shaper) *Context {
	c := &Context{
		sh:        sh,
		WheelStep: 48,
		Theme:     Theme{HotOverlay: 0xFFFFFF1C, ActiveOverlay: 0x00000026},
		Rates:     Rates{Hot: 50, Active: 50, Focus: 50, Scroll: 40},
		pool:      core.NewPool(256),
		table:     core.NewTable(256),
		avPool:    core.NewPool(16),
		avTable:   core.NewTable(16),
		boxes:     make([]box, 1, 256),
		order:     make([]ID, 0, 256),
		prevOrder: make([]ID, 0, 256),
	}
	c.pool.Alloc() // slot 0 is the nil ID
	c.avPool.Alloc()
	c.av = make([]animEnt, 1, 16)
	c.fmt.a = &c.fmtA
	c.sBG.init([4]uint32{})
	c.sText.init(0xFFFFFFFF)
	c.sBorder.init(border{})
	c.sRadii.init([4]float32{})
	c.sShadow.init([2]Shadow{})
	c.sFont.init(0)
	c.sAlign.init(AlignLeft)
	c.sPad.init([4]float32{})
	c.sGap.init(0)
	c.sAxis.init(AxisY)
	c.sW.init(Size{})
	c.sH.init(Size{})
	c.sOpacity.init(1)
	c.sTint.init(Tint{})
	return c
}

// Reserve pre-sizes the box cache for n simultaneously live boxes so that
// building up to n boxes never grows it.
func (c *Context) Reserve(n int) {
	c.table.Reserve(n)
	if cap(c.boxes) < n+1 {
		nb := make([]box, len(c.boxes), n+1)
		copy(nb, c.boxes)
		c.boxes = nb
	}
	if cap(c.order) < n {
		c.order = append(make([]ID, 0, n), c.order...)
	}
	if cap(c.prevOrder) < n {
		c.prevOrder = append(make([]ID, 0, n), c.prevOrder...)
	}
}

// Scale is the pixels-per-point factor passed to Begin; Pt converts points.
func (c *Context) Scale() float32       { return c.scale }
func (c *Context) Pt(v float32) float32 { return v * c.scale }

// Size is the window size in pixels passed to Begin.
func (c *Context) Size() (w, h int) { return c.w, c.h }

// Input is the input of the current frame.
func (c *Context) Input() *Input { return &c.in }

// Root is the root box of the frame, sized to the window. Boxes built as its
// direct children with FloatingX/FloatingY are positioned in window
// coordinates (popups, overlays).
func (c *Context) Root() ID { return c.root }

// Boxes is the number of boxes built this frame (valid after End too).
func (c *Context) Boxes() int { return len(c.order) }

// Live is the number of boxes in the cache.
func (c *Context) Live() int { return c.pool.Len() - 1 }

var rootKey = HashString(0, "ui:root")

// Begin starts a frame. in is copied. widthPx and heightPx are the drawable
// size, scale the pixels per point and dt the seconds since the previous
// frame. It hit-tests the pointer against the previous frame's layout and
// applies mouse buttons, focus and scrolling.
func (c *Context) Begin(in *Input, widthPx, heightPx int, scale, dt float32) {
	c.frame++
	c.in = *in
	c.w, c.h, c.scale, c.dt = widthPx, heightPx, scale, dt
	c.fr.Swap()
	c.fmtA.Reset()
	c.prevOrder, c.order = c.order, c.prevOrder[:0]
	c.animating = false
	c.rHot = rate(c.Rates.Hot, dt)
	c.rActive = rate(c.Rates.Active, dt)
	c.rFocus = rate(c.Rates.Focus, dt)
	c.rScroll = rate(c.Rates.Scroll, dt)

	c.hitTest()
	c.mouse()
	c.stepScroll()

	c.nparents = 0
	c.resetStyles()
	root := c.newBox(0, rootKey)
	b := &c.boxes[root]
	b.pref = [2]Size{Px(float32(widthPx)), Px(float32(heightPx))}
	c.root = root
	c.parents[0] = root
	c.nparents = 1
}

func (c *Context) resetStyles() {
	c.sBG.reset()
	c.sText.reset()
	c.sBorder.reset()
	c.sRadii.reset()
	c.sShadow.reset()
	c.sFont.reset()
	c.sAlign.reset()
	c.sPad.reset()
	c.sGap.reset()
	c.sAxis.reset()
	c.sW.reset()
	c.sH.reset()
	c.sOpacity.reset()
	c.sTint.reset()
	c.hasNextPos = false
}

// Push makes id the parent of the boxes built next.
func (c *Context) Push(id ID) {
	if c.nparents >= maxDepth {
		panic("ui: box nesting too deep")
	}
	c.parents[c.nparents] = id
	c.nparents++
}

// Pop undoes the last Push.
func (c *Context) Pop() {
	if c.nparents <= 1 {
		panic("ui: unbalanced Pop")
	}
	c.nparents--
}

// Parent is the box that new boxes are added to.
func (c *Context) Parent() ID {
	if c.nparents == 0 {
		return 0
	}
	return c.parents[c.nparents-1]
}

// ParentKey is the key of Parent.
func (c *Context) ParentKey() Key { return c.boxes[c.Parent()].key }

// Open builds a box and makes it the parent (Box followed by Push).
func (c *Context) Open(flags Flags, label string) ID {
	id := c.Box(flags, label)
	c.Push(id)
	return id
}

// Close ends an Open.
func (c *Context) Close() { c.Pop() }

// Box builds a box below the current parent. The label follows the RAD
// rules (SplitLabel): the text before "##" is displayed (with DrawText) and
// the key is hash(parent key, identity).
func (c *Context) Box(flags Flags, label string) ID {
	display, ident := SplitLabel(label)
	id := c.newBox(flags, HashString(c.ParentKey(), ident))
	if flags&DrawText != 0 && len(display) > 0 {
		c.setText(id, c.fr.Cur().AppendString(display))
	}
	c.resolveAuto(id)
	return id
}

// BoxKeyed builds a box with an explicit key (see KeyN, HashString and
// ParentKey) and display bytes, for identities that are not strings.
func (c *Context) BoxKeyed(flags Flags, key Key, display []byte) ID {
	id := c.newBox(flags, key)
	if flags&DrawText != 0 && len(display) > 0 {
		c.setText(id, c.fr.Cur().AppendBytes(display))
	}
	c.resolveAuto(id)
	return id
}

// Label builds a text box: Box(DrawText, label) with the sizes left on auto.
func (c *Context) Label(label string) ID { return c.Box(DrawText, label) }

// Spacer builds an invisible box of the given size.
func (c *Context) Spacer(w, h Size) ID {
	c.NextSize(w, h)
	return c.Box(0, "")
}

func (c *Context) newBox(flags Flags, key Key) ID {
	var id ID
	found := false
	for n := uint64(0); ; n++ {
		v, ok := c.table.Get(uint64(key))
		if !ok {
			break
		}
		if c.boxes[v].lastFrame != c.frame {
			id, found = ID(v), true
			break
		}
		key = KeyN(key, n) // duplicate key among this frame's boxes: derive a distinct one
	}
	if !found {
		id = ID(c.pool.Alloc().Index())
		if int(id) >= len(c.boxes) {
			c.boxes = append(c.boxes, box{})
		}
		c.table.Put(uint64(key), uint32(id))
		c.boxes[id].persist = persist{key: key}
	}
	b := &c.boxes[id]
	p := b.persist
	*b = box{persist: p}
	b.lastFrame = c.frame
	b.flags = flags

	parent := c.Parent()
	b.parent = parent
	if parent != 0 {
		pb := &c.boxes[parent]
		if pb.last != 0 {
			c.boxes[pb.last].next = id
			b.prev = pb.last
		} else {
			pb.first = id
		}
		pb.last = id
	}
	c.order = append(c.order, id)

	b.bg = c.sBG.take()
	b.textColor = c.sText.take()
	bd := c.sBorder.take()
	b.borderColor, b.borderW = bd.Color, bd.Width
	b.radii = c.sRadii.take()
	b.shadow = c.sShadow.take()
	b.tint = c.sTint.take()
	b.opacity = c.sOpacity.take()
	b.font = c.sFont.take()
	b.align = c.sAlign.take()
	b.axis = c.sAxis.take()
	b.pad = c.sPad.take()
	b.gap = c.sGap.take()
	b.pref = [2]Size{c.sW.take(), c.sH.take()}
	if c.hasNextPos {
		b.fixed = c.nextPos
		c.hasNextPos = false
	}
	return id
}

// setText stores the display span and shapes it.
func (c *Context) setText(id ID, sp core.Span) {
	b := &c.boxes[id]
	b.txt = sp
	ref := c.sh.Shape(b.font, c.fr.Cur().Bytes(sp))
	b.run = ref
	b.textW = c.sh.Run(ref).Advance
	b.runGen = c.sh.RunGeneration()
}

func (c *Context) resolveAuto(id ID) {
	b := &c.boxes[id]
	for a := 0; a < 2; a++ {
		if b.pref[a].Kind == SizeAuto {
			if b.txt.Len > 0 {
				b.pref[a] = TextSize(0)
			} else {
				b.pref[a] = Children(1)
			}
		}
	}
}

func (c *Context) fontInfo(f text.FontKey) fontInfo {
	if int(f) >= len(c.fonts) {
		c.fonts = append(c.fonts, make([]fontInfo, int(f)+1-len(c.fonts))...)
	}
	fi := &c.fonts[f]
	if !fi.ok {
		m := c.sh.Metrics(f)
		lh := m.LineHeight
		if lh <= 0 {
			lh = m.Ascent + m.Descent + m.LineGap
		}
		*fi = fontInfo{ok: true, ascent: m.Ascent, descent: m.Descent, lh: lh}
	}
	return *fi
}

// KeyOf is the key of box id.
func (c *Context) KeyOf(id ID) Key { return c.boxes[id].key }

// Rect is the screen rectangle (x0, y0, x1, y1) box id had after the last
// layout; during a build that is the previous frame's.
func (c *Context) Rect(id ID) [4]float32 { return c.boxes[id].rect }

func rate(k, dt float32) float32 {
	if k <= 0 || dt <= 0 {
		return 0
	}
	return float32(1 - math.Exp2(float64(-k*dt)))
}
