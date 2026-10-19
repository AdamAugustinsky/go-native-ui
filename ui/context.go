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
	// savedFocus is, for an overlay root, the focus when it opened.
	savedFocus Key
	// wrap is what the line breaking of a WrapText box was computed for.
	wrap wrapCache
	// rp is the last layout of a RichText box (hit testing), pressSpan 1 +
	// the span its press began on.
	rp        richPrev
	pressSpan int32
}

// box is one record of the pointer-free box pool. Everything after persist
// is rebuilt each frame by the builder.
type box struct {
	persist

	flags                           Flags
	parent, first, last, next, prev ID
	field                           bool  // a text field (TextField): it takes the keys of keys.go when focused
	escClears                       bool  // a text field that clears itself on Escape and has text to clear (NextEscapeClears)
	ovRoot                          bool  // the root box of an overlay
	ovl                             uint8 // the overlay the box belongs to, 1 + its index in the frame's overlays; 0 for the main tree

	bg          [4]uint32
	textColor   uint32
	borderColor uint32
	borderW     float32
	sides       Sides
	cursor      Cursor
	img         Image
	radii       [4]float32
	shadow      [2]Shadow
	tint        Tint
	opacity     float32
	font        text.FontKey
	align       Align
	axis        Axis
	cross       Cross
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

	sem int32 // 1 + the index of its record in Context.sems, 0 without a role

	// the paragraph of a WrapText or RichText box: lnN lines, laid out at
	// width paraW (-1: not yet this frame) in the cache generation paraGen
	lnN     int32
	para    text.ParaRef
	paraGen uint32
	paraW   float32
	rich    int32 // 1 + the index of its record in Context.rich, 0 for other boxes
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
	// WheelJumps makes a wheel jump straight to where it scrolls, as AppKit's
	// does (app sets it on macOS); otherwise the offset glides there (Rates.Scroll).
	WheelJumps bool
	// TextEdit styles the text fields (colours and caret blink); see TextField.
	TextEdit TextEditStyle
	// ClipboardSet puts plain UTF-8 text on the system clipboard for the copy
	// and cut of text fields; it must copy b, which aliases the field's text.
	// The app sets it (app.Run does); a nil function disables copy and cut.
	// Pasting is asynchronous: see PasteRequest and Input.SetPaste.
	ClipboardSet func(b []byte)

	ap  appearance  // system appearance in, the shown mode out (appearance.go)
	win windowState // the window's decorations and buttons in, requests out (window.go)

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
	sSides   stack[Sides]
	sCursor  stack[Cursor]
	sImage   stack[Image]
	sRadii   stack[[4]float32]
	sShadow  stack[[2]Shadow]
	sFont    stack[text.FontKey]
	sAlign   stack[Align]
	sPad     stack[[4]float32]
	sGap     stack[float32]
	sAxis    stack[Axis]
	sCross   stack[Cross]
	sW, sH   stack[Size]
	sOpacity stack[float32]
	sTint    stack[Tint]

	nextPos    [2]float32
	hasNextPos bool
	nextFlags  Flags

	// paragraphs (wrap.go, richtext.go): the layouter, how often a wrapped
	// box was laid out (not taken from the cache), and this frame's RichText
	// records with their spans and styles
	lay        Layouter
	wrapRuns   uint64
	oneFont    [1]text.FontKey
	para       text.Para // the input of the layout in progress
	rects      [][4]float32
	rich       []richRec
	richSpans  []Span
	richStyles []RichStyle

	// semantics (semantics.go): the setters for the next box, and the records
	// of this frame's boxes that have a role
	semNext semNext
	sems    []semRec
	semA    core.Arena // the names and values of this frame's semantics

	// interaction (see signal.go)
	hotKey, scrollKey Key
	hotID             ID
	active            [2]Key
	pressedKey        [2]Key
	releasedKey       [2]Key
	pressPos          [2][2]float32
	pressClicks       [2]uint8
	relPos            [2][2]float32 // the origin and click count of the press that this frame's release ended
	relClicks         [2]uint8
	focusKey          Key
	focusVisible      bool // focus came from the keyboard (FocusVisible)
	scrollSet         bool // ScrollTo was called this frame (End decides whether the scroll is still moving)

	sp scrollPhys // trackpad gestures, latching and rubber bands (scrollphys.go)

	// animation (see anim.go)
	animating             bool
	rHot, rActive, rFocus float32
	rScroll               float32
	av                    []animEnt
	avPool                *core.Pool
	avTable               *core.Table

	// text fields (textfield.go): the earliest time a field wants a frame
	// again (ns from the frame start, 0 = none), and the input-method
	// rectangle of the focused field.
	wakeNs int64
	// deadlines (deadline.go) are the named one-shot deadlines in force.
	deadlines []deadline
	imeRect   [4]float32
	imeOK     bool
	// imeOwner is the field the input method's composition (Input.Preedit)
	// belongs to, imeCancel whether this frame found the composition
	// orphaned (its field lost the focus or is gone).
	imeOwner  Key
	imeCancel bool
	// commit[:nCommit] is the text that ended the composition this frame
	// (taken out of the frame's Input.Text), for commitOwner, the field that
	// owned that composition. A preedit that is present with a commit is a new
	// composition: imeOwner starts again from nobody.
	commit      [MaxText]byte
	nCommit     int
	commitOwner Key

	// overlays (overlay.go): this frame's, in build order, and the previous
	// frame's (what hit testing and the input steps of Begin see)
	ovs        [maxOverlays]overlay
	nOvs       int
	ovPrev     [maxOverlays]overlay
	nOvPrev    int
	ovOpen     [maxOverlays]int // c.nparents at each open BeginOverlay
	nOvOpen    int
	parentless bool // the next box has no parent (an overlay root)
	modalIdx   int  // the topmost modal of the previous frame, -1 none
	// the popups the user closed this frame (OverlaySignal.Dismissed), and
	// whether this frame's press was consumed by closing them
	dismissed     [maxOverlays]Key
	nDismiss      int
	pressConsumed bool

	// key claiming and edit actions (keys.go)
	claimed    uint16              // bit i: key event i is handled
	fieldKeys  uint16              // bit i: key event i was claimed for the focused text field
	nextEscClr bool                // the next TextField clears itself on Escape (NextEscapeClears)
	nextStill  bool                // the next TextField does not animate hover (NextFieldNoHover)
	fieldPeek  uint16              // bit i: key event i (Escape) is seen by the focused field in an overlay but not claimed
	fieldFocus bool                // the focused box is a text field that takes keys
	fieldBuilt bool                // the focused field applied this frame's edit actions already
	fieldKey   Key                 // the key of that field
	fieldOwner Key                 // the text field that had the focus at Begin and owns this frame's keys and edit actions; 0 none
	carry      [MaxEdit]EditAction // edit actions queued after the field was built: for the next frame
	nCarry     int
	cmdTaken   int // Input.Commands taken so far this frame (TakeCommand)
	activated  Key // the box that took its Enter/Space activation this frame (Signal)
	// a text field asked for the clipboard text (PasteRequest)
	pasteTag Key
	pasteReq bool

	// native menus (nativemenu.go)
	nativeMenus bool
	menuReq     MenuRequest
	menuReqSet  bool
	menuOpts    []string // the copy of menuReq.Options

	// boxes built this frame with a cursor or the WindowDrag flag (cursor.go)
	nCursorBoxes, nDragBoxes int

	// the final emit attempt of End (draw.go)
	finalPass      bool
	passGen        uint32  // atlas generation the glyph quads so far belong to
	glyphQuads     []int32 // indices of the glyph quads emitted so far
	droppedGlyphs  bool
	overflowRun    int    // frames in a row that dropped glyphs
	overflowFrames uint64 // ... in total

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
		TextEdit:  defaultTextEdit,
		pool:      core.NewPool(256),
		table:     core.NewTable(256),
		avPool:    core.NewPool(16),
		avTable:   core.NewTable(16),
		boxes:     make([]box, 1, 256),
		order:     make([]ID, 0, 256),
		prevOrder: make([]ID, 0, 256),
		modalIdx:  -1, // no modal before the first frame
	}
	if l, ok := sh.(Layouter); ok {
		c.lay = l
	} else {
		c.lay = text.NewBasicLayouter(sh)
	}
	c.pool.Alloc() // slot 0 is the nil ID
	c.avPool.Alloc()
	c.av = make([]animEnt, 1, 16)
	c.fmt.a = &c.fmtA
	c.sBG.init([4]uint32{})
	c.sText.init(0xFFFFFFFF)
	c.sBorder.init(border{})
	c.sSides.init(SidesAll)
	c.sCursor.init(CursorArrow)
	c.sImage.init(Image{})
	c.sRadii.init([4]float32{})
	c.sShadow.init([2]Shadow{})
	c.sFont.init(0)
	c.sAlign.init(AlignLeft)
	c.sPad.init([4]float32{})
	c.sGap.init(0)
	c.sAxis.init(AxisY)
	c.sCross.init(CrossStart)
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
	c.pool.Reserve(n + 1)
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
func (c *Context) Scale() float32 { return c.scale }

// Pt converts v from points to pixels with the scale given to Begin.
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
	c.pruneDeadlines()
	c.w, c.h, c.scale, c.dt = widthPx, heightPx, scale, dt
	c.fr.Swap()
	c.fmtA.Reset()
	c.prevOrder, c.order = c.order, c.prevOrder[:0]
	c.animating, c.scrollSet = false, false
	c.wakeNs, c.imeOK, c.imeCancel = 0, false, false
	c.takeCommit()
	c.rich, c.richSpans, c.richStyles = c.rich[:0], c.richSpans[:0], c.richStyles[:0]
	c.ovPrev, c.nOvPrev = c.ovs, c.nOvs
	c.nOvs, c.nOvOpen, c.parentless = 0, 0, false
	c.modalIdx = c.topModal()
	c.claimed, c.cmdTaken, c.activated = 0, 0, 0
	c.fieldBuilt, c.pasteReq, c.pasteTag = false, false, 0
	for i := 0; i < c.nCarry; i++ { // edit actions the previous frame could not give its field
		c.in.PushEdit(c.carry[i])
	}
	c.nCarry = 0
	if in.NPreedit == 0 || c.nCommit > 0 {
		c.imeOwner = 0
	}
	c.nCursorBoxes, c.nDragBoxes = 0, 0
	c.rHot = rate(c.Rates.Hot, dt)
	c.rActive = rate(c.Rates.Active, dt)
	c.rFocus = rate(c.Rates.Focus, dt)
	c.rScroll = rate(c.Rates.Scroll, dt)

	c.hitTest()
	c.overlayInput()
	c.mouse()
	c.claimFieldKeys()
	c.overlayEscape()
	c.semActions()
	c.stepScroll()

	c.nparents = 0
	c.resetStyles()
	c.sems = c.sems[:0]
	c.semA.Reset()
	c.semNext = semNext{role: RoleWindow}
	root := c.newBox(0, rootKey)
	b := &c.boxes[root]
	b.pref = [2]Size{Px(float32(widthPx)), Px(float32(heightPx))}
	c.root = root
	c.parents[0] = root
	c.nparents = 1
}

// takeCommit moves the text that committed an input-method composition out of
// the frame's typed text, when a field is known to own the composition (the one
// that showed it last frame). The owner inserts it wherever the focus is by now
// (TextField), a frame with no such field drops it: it must never land in
// whichever other field has the focus, nor be seen as typed text (type-to-filter
// of the app). Without an owner the text is ordinary typed text.
func (c *Context) takeCommit() {
	in := &c.in
	off, n := in.CommitOff, in.CommitLen
	in.CommitOff, in.CommitLen = 0, 0
	// A commit longer than one frame's text goes on in the next frame, for the
	// same owner (the platform layer tags the rest too).
	owner := c.imeOwner
	if c.nCommit > 0 {
		owner = c.commitOwner
	}
	c.nCommit, c.commitOwner = 0, 0
	if n <= 0 || off < 0 || off+n > in.NText || owner == 0 {
		return
	}
	c.nCommit = copy(c.commit[:], in.Text[off:off+n])
	c.commitOwner = owner
	copy(in.Text[off:], in.Text[off+n:in.NText])
	in.NText -= n
}

func (c *Context) resetStyles() {
	c.sBG.reset()
	c.sText.reset()
	c.sBorder.reset()
	c.sSides.reset()
	c.sCursor.reset()
	c.sImage.reset()
	c.sRadii.reset()
	c.sShadow.reset()
	c.sFont.reset()
	c.sAlign.reset()
	c.sPad.reset()
	c.sGap.reset()
	c.sAxis.reset()
	c.sCross.reset()
	c.sW.reset()
	c.sH.reset()
	c.sOpacity.reset()
	c.sTint.reset()
	c.hasNextPos = false
	c.nextFlags = 0
	c.nextEscClr = false
	c.nextStill = false
	c.semNext = semNext{}
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
	flags |= c.nextFlags
	c.nextFlags = 0
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
	if c.parentless {
		parent = 0
	}
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
	if parent != 0 {
		b.ovl = c.boxes[parent].ovl
	}

	b.bg = c.sBG.take()
	b.textColor = c.sText.take()
	bd := c.sBorder.take()
	b.borderColor, b.borderW = bd.Color, bd.Width
	b.sides = c.sSides.take()
	b.cursor = c.sCursor.take()
	if b.cursor != CursorArrow {
		c.nCursorBoxes++
	}
	if flags&WindowDrag != 0 {
		c.nDragBoxes++
	}
	b.img = c.sImage.take()
	b.radii = c.sRadii.take()
	b.shadow = c.sShadow.take()
	b.tint = c.sTint.take()
	b.opacity = c.sOpacity.take()
	b.font = c.sFont.take()
	b.align = c.sAlign.take()
	b.axis = c.sAxis.take()
	b.cross = c.sCross.take()
	b.pad = c.sPad.take()
	b.gap = c.sGap.take()
	b.pref = [2]Size{c.sW.take(), c.sH.take()}
	if c.hasNextPos {
		b.fixed = c.nextPos
		c.hasNextPos = false
	}
	if n := &c.semNext; n.role != RoleNone {
		c.sems = append(c.sems, semRec{id: id, role: n.role, state: n.state, actions: n.actions, hasActions: n.hasActions,
			name: n.name, value: n.value, rng: n.rng, hasRange: n.hasRange, pos: n.pos, hasPos: n.hasPos})
		b.sem = int32(len(c.sems))
	}
	c.semNext = semNext{}
	return id
}

// setText stores the display span and shapes it (a WrapText box is laid out
// as a paragraph instead, in End).
func (c *Context) setText(id ID, sp core.Span) {
	b := &c.boxes[id]
	b.txt = sp
	b.paraW = -1
	if b.flags&WrapText != 0 {
		return
	}
	ref := c.sh.Shape(b.font, c.fr.Cur().Bytes(sp))
	b.run = ref
	b.textW = c.sh.Run(ref).Advance
	b.runGen = c.sh.RunGeneration()
}

func (c *Context) resolveAuto(id ID) {
	b := &c.boxes[id]
	for a := 0; a < 2; a++ {
		if b.pref[a].Kind == SizeAuto {
			if a == 0 && b.flags&WrapText != 0 && b.txt.Len > 0 {
				b.pref[a] = Pct(1) // wrapped text fills the width it is given
			} else if b.txt.Len > 0 {
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
