package ui

import (
	"math"

	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/text"
)

const maxDepth = 64

type truncCache struct {
	run, ellipsis text.RunRef
	gen           uint32
	w             float32
	n             int32
	ready, ok     bool
}

// persist is the part of a box record that survives from frame to frame while
// the box keeps being built, including the previous layout's results (which
// hit testing, ListRange and scroll clamping read during the next build).
type persist struct {
	key       Key
	lastFrame uint32
	cut       uint32 // 1 + the sparse truncation-cache slot, 0 without one
	hotT      float32
	activeT   float32
	focusT    float32
	// savedFocus is, for an overlay root, the focus when it opened.
	savedFocus Key
	// wrap is what the line breaking of a WrapText box was computed for.
	wrap wrapCache
	// flowOut is what the last layout of a flow box found (flow.go).
	flowOut flowOut
	// hid is whether the last layout hid the box (flow.go); newBox moves it to
	// the box's prevHid and clears it for the frame being laid out.
	hid bool
	// prevTooltip keeps the last declaration when this box is rebuilt, so
	// nested tooltip priority does not depend on declaration order.
	prevTooltip bool
	// rp is the last layout of a RichText box (hit testing), pressSpan 1 +
	// the span its press began on.
	rp        richPrev
	pressSpan int32

	// The layout results sit next to the frame's layout inputs.
	scroll    [2]float64 // current offset, pixels
	scrollTgt [2]float64
	content   [2]float64 // extent of the children incl. padding (virtual extent for lists)
	rect      [4]float32 // snapped screen rectangle
	size      [2]float32
	origin    [2]float32 // unsnapped top-left
}

// box is one record of the pointer-free box pool. Everything after persist
// is rebuilt each frame by the builder.
type box struct {
	persist
	boxFrame
}

// boxFrame is the part of a box record rebuilt every frame.
type boxFrame struct {
	boxLinks
	style
	boxAttrs
}

type boxLinks struct {
	flags                           Flags
	parent, first, last, next, prev ID
	field                           bool  // a text field (TextField): it takes the keys of keys.go when focused
	escClears                       bool  // a text field that clears itself on Escape and has text to clear (NextEscapeClears)
	secure                          bool  // a secure text field (NextSecureField): it shows bullets
	ovRoot                          bool  // the root box of an overlay
	ovl                             uint8 // the overlay the box belongs to, 1 + its index in the frame's overlays; 0 for the main tree
	mat                             uint8 // 1 + the box's record in shell.mats when Material marked it (material.go)
	bdrop                           uint8 // 1 + the box's recipe in Context.bd.recs when it is a backdrop (NextBackdrop)
	tooltip                         bool  // declared as a drawn-tooltip owner this frame
}

// boxAttrs excludes the style, which newBox overwrites rather than clears.
type boxAttrs struct {
	dupLast   ID // last box in this key's derivation chain this frame
	dupN      uint32
	alignNext bool       // the alignment came from NextAlign (Audit)
	auditOff  AuditMask  // the checks of Audit the box is exempt from (NextAuditExempt)
	fadeEnd   float32    // its children fade out over its last fadeEnd pixels (NextFadeEnd)
	obscured  [2]float32 // a scroll box's view covered at its top and bottom (SetObscured)
	stopWheel bool       // the wheel over the box scrolls nothing beneath it (StopWheel)
	fixed     [2]float32
	anchor    Anchor     // NextAnchor: fixed holds the insets
	lo, hi    [2]float32 // NextMinSize, NextMaxSize (0: none)
	self      Cross      // NextAlignSelf: 1 + the Cross, 0 the parent's
	nudge     [2]float32 // NextOffset: moved from its laid-out place

	txt    core.Span // display text in the current frame's arena
	run    text.RunRef
	runGen uint32
	textW  float32

	listRows  int
	listFirst int
	listRowH  float32

	sem int32 // 1 + the index of its record in Context.sems, 0 without a role

	flow    flowSpec // the options of a flow box (AxisFlow, flow.go)
	grid    gridSpec // the options of a grid box (AxisGrid, grid.go)
	prevHid bool     // the previous layout's hid: what the input checks of the build see (wasHidden)

	// the paragraph of a WrapText or RichText box: lnN lines, laid out at
	// width paraW (-1: not yet this frame) in the cache generation paraGen
	lnN     int32
	para    text.ParaRef
	paraGen uint32
	paraW   float32
	rich    int32 // 1 + the index of its record in Context.rich, 0 for other boxes
}

// style is copied from the mirrored stack tops when a box is built.
type style struct {
	pref      [2]Size
	pad       [4]float32 // l, t, r, b
	gap       float32
	axis      Axis
	cross     Cross
	justify   Justify
	align     Align
	font      text.FontKey
	bg        [4]uint32
	textColor uint32
	bd        border
	sides     Sides
	cursor    Cursor
	img       Image
	radii     [4]float32
	shadow    [2]Shadow
	tint      Tint
	opacity   float32
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

	sh        Shaper
	shapeInfo interface {
		ShapeInfo(text.FontKey, []byte) (text.RunRef, float32, uint32)
	}
	fieldCarets text.CaretIndex // the focused field's geometry, independent of run-cache storage
	fieldRun    text.RunRef
	fieldRunGen uint32

	boxes                []box
	pool                 *core.Pool
	table                *core.Table
	order                []ID // boxes built this frame, parents before children
	prevOrder            []ID
	scrollBoxes          []ID // scroll boxes of the last completed build
	reserved, trimFrames int
	frLow, fmtLow        int
	cuts                 []truncCache
	cutPool              *core.Pool
	root                 ID
	frame                uint32
	w, h                 int
	scale, dt            float32
	in                   Input
	fr                   core.Frame
	fmtA                 core.Arena
	fmt                  Fmt

	parents  [maxDepth]ID
	nparents int

	cur        style    // the tops of the style stacks
	nextStyles uint32   // bit per style stack with a next value
	styleOwner *Context // the address the stack mirrors are bound to

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
	sJustify stack[Justify]
	sW, sH   stack[Size]
	sOpacity stack[float32]
	sTint    stack[Tint]

	nextPos    [2]float32
	hasNextPos bool
	nextAnchor Anchor
	nextInset  [2]float32
	nextNudge  [2]float32
	nextLo     [2]float32
	nextHi     [2]float32
	nextSelf   Cross // 1 + the Cross; 0 none
	nextFlags  Flags
	nextAudit  AuditMask  // the exemptions of the next box (NextAuditExempt)
	nextFade   float32    // the next box's children's fade at its end (NextFadeEnd)
	emitFade   [2]float32 // while emitting: the fade of the boxes round this one (NextFadeEnd)
	bd         backdrops  // this frame's backdrops and the next box's (NextBackdrop)
	mesh       meshes     // this frame's borrowed quad meshes (mesh.go)

	flowHidden bool // a flow hid children this layout (flow.go)

	// paragraphs (wrap.go, richtext.go): the layouter, how often a wrapped
	// box was laid out (not taken from the cache), and this frame's RichText
	// records with their spans and styles
	lay        Layouter
	wrapRuns   uint64
	oneFont    [1]text.FontKey
	para       text.Para                   // the input of the layout in progress
	layFonts   [MaxRichStyles]text.FontKey // LayoutText's fonts
	rects      [][4]float32
	rich       []richRec
	richViews  []richViewCache // views stay outside the pointer-free rich records
	richSpans  []Span
	richStyles []RichStyle
	richFocus  []richFocus // span targets; retained through Begin's AX requests
	sel        selState    // selection scopes (selection.go)

	// semantics (semantics.go): the setters for the next box, and the records
	// of this frame's boxes that have a role
	semTrack semTracking
	regions  frameRegions
	semNext  semNext
	sems     []semRec
	semA     core.Arena // the names and values of this frame's semantics
	semX     semExtra   // announcements (semantics_a11y.go)

	// interaction (see signal.go)
	hotKey, scrollKey Key
	hotID             ID
	tooltipKey        Key // pointer hit including disabled boxes; never drives interaction
	tooltipID         ID  // scratch of hitWalk; only hitTest publishes tooltipKey
	active            [2]Key
	pressedKey        [2]Key
	releasedKey       [2]Key
	pressPos          [2][2]float32
	pressClicks       [2]uint8
	relPos            [2][2]float32 // the origin and click count of the press that this frame's release ended
	relClicks         [2]uint8
	focusKey          Key
	richFocusKey      Key  // last focus key known to name a rich span, not a pending box
	focusVisible      bool // focus came from the keyboard (FocusVisible)
	textOnlyTab       bool // Tab skips the controls (focus classes, focus.go): SetTabReachesControls(false)
	scrollSet         bool // ScrollTo was called this frame (End decides whether the scroll is still moving)

	sp scrollPhys   // trackpad gestures, latching and rubber bands (scrollphys.go)
	gs gestureState // pinch and rotation latching (gesture.go)

	swapBuf []byte // scratch for EditTranspose (keymoves.go): kept, so a swap of long clusters allocates only while it grows

	// animation (see anim.go)
	animating             bool
	rHot, rActive, rFocus float32
	rScroll               float32
	av                    []animEnt
	avPool                *core.Pool
	avTable               *core.Table
	avOrder, avPrev       []uint32

	// text fields (textfield.go): the earliest time a field wants a frame
	// again (ns from the frame start, 0 = none), and the input-method
	// rectangle of the focused field.
	wakeNs int64

	st settleState // the build's reads of the previous layout (settle.go)

	shownWatch []shownWatch // WatchShown's boxes of this frame, checked after layout
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
	claimed    uint16         // bit i: key event i is handled
	fieldKeys  uint16         // bit i: key event i was claimed for the focused text field
	nextEscClr bool           // the next TextField clears itself on Escape (NextEscapeClears)
	nextStill  bool           // the next TextField does not animate hover (NextFieldNoHover)
	nextSecure bool           // the next TextField is a secure field (NextSecureField)
	secureOn   bool           // a secure field has the focus in the focused window, as this frame built it (SecureFieldFocused)
	nextCmdr   FieldCommander // the next TextField offers its commands to this (NextFieldCommander)
	nextSeps   string         // ... and its separator characters
	cpNow      colorReq       // the colour panel request of the frame being built (ColorPanel)
	cpLast     colorReq       // ... and of the frame before
	nextOvl    OverlayOpts    // the options of the next popup overlay (NextOverlay)
	cmdr       FieldCommander // the commander of the field being built, while it processes its input
	cmdrSeps   string
	fieldPeek  uint16              // bit i: key event i (Escape) is seen by the focused field in an overlay but not claimed
	fieldFocus bool                // the focused box is a text field that takes keys
	fieldBuilt bool                // the focused field applied this frame's edit actions already
	fieldKey   Key                 // the key of that field
	fieldOwner Key                 // the text field that had the focus at Begin and owns this frame's keys and edit actions; 0 none
	carry      [MaxEdit]EditAction // edit actions queued after the field was built: for the next frame
	nCarry     int
	held       [MaxScrolls]ScrollEvent // scroll events kept for the frame after a scale change (holdScroll)
	nHeld      int
	cmdTaken   int // Input.Commands taken so far this frame (TakeCommand)
	activated  Key // the box that took its Enter/Space activation this frame (Signal)
	// a text field asked for the clipboard text (PasteRequest)
	pasteTag Key
	pasteReq bool

	nt nativeTips // native tooltips declared this frame (regions_native.go)

	drop dropState // drop targets and drags out (drag.go)
	// native menus (nativemenu.go)
	nativeMenus bool
	menuReq     MenuRequest
	menuReqSet  bool
	menuOpts    []string // the copy of menuReq.Options

	// boxes built this frame with a cursor or the WindowDrag flag (cursor.go)
	nCursorBoxes, nDragBoxes int

	// material and host boxes marked this frame (material.go, host.go)
	shell shellRegs

	// the final emit attempt of End (draw.go)
	finalPass      bool
	passGen        uint32  // atlas generation the glyph quads so far belong to
	glyphQuads     []int32 // indices of the glyph quads emitted so far
	droppedGlyphs  bool
	overflowRun    int    // frames in a row that dropped glyphs
	overflowFrames uint64 // ... in total

	fonts   []fontInfo
	fontGen uint32 // the text system's RunGeneration fonts was read at

	dbg *debugState // the recording of EnableDebug (debug.go); nil off

	up           Uploader
	pageTex      []gpu.TextureID
	pageUploaded []bool       // first upload is immediate; subsequent changes are staged
	shared       *SharedAtlas // the page textures shared with other contexts (ShareAtlas), nil: pageTex
	texOK        []bool

	widgetUp Uploader // what Uploader gives the widgets, nil: up (SetWidgetUploader)
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
		cutPool:   core.NewPool(16),
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
	c.shapeInfo, _ = sh.(interface {
		ShapeInfo(text.FontKey, []byte) (text.RunRef, float32, uint32)
	})
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
	c.sJustify.init(JustifyStart)
	c.sW.init(Size{})
	c.sH.init(Size{})
	c.sOpacity.init(1)
	c.sTint.init(Tint{})
	c.bindStyles()
	return c
}

// Reserve pre-sizes the box cache for n simultaneously live boxes so that
// building up to n boxes never grows it. Automatic trimming keeps this floor.
func (c *Context) Reserve(n int) {
	c.reserved = max(c.reserved, n)
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
	if cap(c.st.reads) < n/4 { // (a build reads the last layout of a few of its boxes)
		c.st.reads = append(make([]layoutRead, 0, n/4), c.st.reads...)
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
	if c.sh != nil {
		if g := c.sh.RunGeneration(); g != c.fontGen {
			// The text system dropped its runs: that happens when it evicts, and
			// when it releases fonts (text.System.ReleaseScale), whose keys then
			// name other fonts. What was read of the fonts is read again.
			clear(c.fonts)
			c.fontGen = g
		}
	}
	old := c.scale
	c.w, c.h, c.scale, c.dt = widthPx, heightPx, scale, dt
	trimScratch(c.fr.Cur(), &c.frLow)
	trimScratch(&c.fmtA, &c.fmtLow)
	c.fr.Reset()
	c.fmtA.Reset()
	c.prevOrder, c.order = c.order, c.prevOrder[:0]
	c.avPrev, c.avOrder = c.avOrder, c.avPrev[:0]
	changed := old > 0 && scale != old // the window's scale changed (a new display, an app zoom)
	if changed {
		c.rescaleScroll(float64(scale) / float64(old))
	}
	c.holdScroll(changed)
	c.animating, c.scrollSet = c.nHeld > 0, false // (held input needs the next frame)
	c.wakeNs, c.imeOK, c.imeCancel, c.secureOn = 0, false, false, false
	c.takeCommit()
	clear(c.richViews)
	c.richViews = c.richViews[:0]
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
	c.dropBegin()
	c.overlayInput()
	c.mouse()
	c.claimFieldKeys()
	c.overlayEscape()
	c.semActions()
	c.rememberRichFocus()
	c.richFocus = c.richFocus[:0]
	c.stepScroll()
	c.trimRetained()
	c.scrollBoxes = c.scrollBoxes[:0]

	c.nparents = 0
	if c.styleOwner != c {
		c.bindStyles()
	}
	c.resetStyles()
	c.semTrack.skip = c.semTrack.disabled
	c.sems = c.sems[:0]
	c.semA.Reset()
	c.semNext = semNext{role: RoleWindow}
	root := c.newBox(0, rootKey)
	b := &c.boxes[root]
	b.pref = [2]Size{Px(float32(widthPx)), Px(float32(heightPx))}
	c.root = root
	c.parents[0] = root
	c.nparents = 1
	c.st.reads, c.st.building = c.st.reads[:0], true
}

// rescaleScroll keeps every scrolled box at the same place in its content
// when the scale changes by the factor r: the offsets are in pixels, so they
// are multiplied, and the view stays on what it showed at the top. The extents
// (content, view) are not: they are not a ratio of the old ones (a viewport
// that fills the window keeps its size in pixels when the zoom grows, a
// virtual list divides by the scale), so they are laid out again by the frame,
// and the frame's scroll input waits for that (holdScroll). The trackpad
// stretches are pixels too.
func (c *Context) rescaleScroll(r float64) {
	for _, id := range c.prevOrder {
		b := &c.boxes[id]
		if b.flags&scrollMask == 0 {
			continue
		}
		for a := 0; a < 2; a++ {
			b.scroll[a] *= r
			b.scrollTgt[a] *= r
		}
	}
	for i := 0; i < c.sp.n; i++ {
		rb := &c.sp.rb[i]
		for a := 0; a < 2; a++ {
			rb.s[a] *= r
			rb.force[a] *= r
			rb.x0[a] *= r
			rb.v0[a] *= r
		}
	}
}

// trimScratch gives a peak buffer back after sixty frames below a quarter
// of its capacity, with room for twice the recent demand.
func trimScratch(a *core.Arena, low *int) {
	n := max(1024, 2*a.Len())
	if n >= a.Cap()/4 {
		*low = 0
		return
	}
	*low++
	if *low == 60 {
		a.Trim(n)
		*low = 0
	}
}

// trimRetained runs between frames, after input has read the last layout.
// IDs belong to one build, so the live cache can be packed here; keys and
// persistent state survive, including the last layout used by the build.
func (c *Context) trimRetained() {
	n := max(256, c.reserved+1, 2*len(c.prevOrder)+1)
	if n >= cap(c.boxes)/4 && n >= cap(c.order)/4 && n >= c.table.Cap()/4 {
		c.trimFrames = 0
		return
	}
	c.trimFrames++
	if c.trimFrames != 60 {
		return
	}
	c.trimFrames = 0
	remap := make([]ID, len(c.boxes))
	boxes := make([]box, len(c.prevOrder)+1, n)
	pool := core.NewPool(n)
	pool.Alloc() // nil ID
	for i, old := range c.prevOrder {
		id := ID(pool.Alloc().Index())
		remap[old] = id
		boxes[id] = c.boxes[old]
		c.prevOrder[i] = id
	}
	c.table.Clear()
	c.table.Trim(n)
	for i := 1; i < len(boxes); i++ {
		b := &boxes[i]
		b.parent, b.first, b.last = remap[b.parent], remap[b.first], remap[b.last]
		b.next, b.prev = remap[b.next], remap[b.prev]
		c.table.Put(uint64(b.key), uint32(i))
	}
	if len(c.cuts) > 0 {
		cuts := make([]truncCache, 0, n)
		c.cutPool = core.NewPool(n)
		for i := 1; i < len(boxes); i++ {
			b := &boxes[i]
			if b.cut != 0 {
				cuts = append(cuts, c.cuts[b.cut-1])
				b.cut = c.cutPool.Alloc().Index() + 1
			}
		}
		c.cuts = cuts
	}
	c.boxes, c.pool = boxes, pool
	c.root, c.hotID, c.tooltipID = remap[c.root], remap[c.hotID], remap[c.tooltipID]
	c.sp.hit, c.sp.aimHit = remap[c.sp.hit], remap[c.sp.aimHit]
	for i := 0; i < c.nOvPrev; i++ {
		c.ovPrev[i].id = remap[c.ovPrev[i].id]
	}
	for i := range c.drop.recs {
		c.drop.recs[i].id = remap[c.drop.recs[i].id]
	}
	for i := range c.nt.recs {
		c.nt.recs[i].id = remap[c.nt.recs[i].id]
	}
	for i := 0; i < c.shell.nMats; i++ {
		c.shell.mats[i].id = remap[c.shell.mats[i].id]
	}
	for i := 0; i < c.shell.nHosts; i++ {
		c.shell.hosts[i].id = remap[c.shell.hosts[i].id]
	}
	// Both order buffers must retain the headroom used by the next frame.
	c.order = make([]ID, 0, n)
	c.prevOrder = append(make([]ID, 0, n), c.prevOrder...)
	c.scrollBoxes = make([]ID, 0, max(16, 2*len(c.scrollBoxes)))
	if cap(c.richViews) > n {
		c.richViews = make([]richViewCache, 0, n)
	}
	if cap(c.st.reads) > n {
		c.st.reads = make([]layoutRead, 0, n/4)
	}
}

// holdScroll keeps the frame's scroll events (wheel, gesture, momentum) for
// the next frame while the scale changes. The scroll limits are those of the
// last layout, which is at the old scale; the first layout at the new scale is
// this frame's, so events applied now would be clamped, or bounced off the
// edge, by bounds that are about to change. They are applied, in order and
// with their phases, in the frame after, against the layout that exists then
// (and the frame is requested). Held events go before the ones that arrive
// meanwhile, and a second scale change holds them again.
func (c *Context) holdScroll(changed bool) {
	in := &c.in
	if !changed && c.nHeld == 0 {
		return
	}
	if in.NScrolls == 0 && (in.ScrollX != 0 || in.ScrollY != 0) { // set without AddScroll
		in.AddScrollEvent(ScrollEvent{DX: in.ScrollX, DY: in.ScrollY, Precise: in.Precise})
	}
	in.ScrollX, in.ScrollY = 0, 0
	if changed { // everything waits, after what already waits
		for i := 0; i < in.NScrolls; i++ {
			addScrollEvent(&c.held, &c.nHeld, in.Scrolls[i])
		}
		in.NScrolls = 0
		return
	}
	// The held events come first.
	var now [MaxScrolls]ScrollEvent
	n := copy(now[:], in.Scrolls[:in.NScrolls])
	in.NScrolls = 0
	for i := 0; i < c.nHeld; i++ {
		in.AddScrollEvent(c.held[i])
	}
	c.nHeld = 0
	for i := 0; i < n; i++ {
		in.AddScrollEvent(now[i])
	}
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

// bindStyles repairs the internal pointers when a Context is initialized by value.
func (c *Context) bindStyles() {
	c.sBG.bind(&c.cur.bg, &c.nextStyles, 1<<0)
	c.sText.bind(&c.cur.textColor, &c.nextStyles, 1<<1)
	c.sBorder.bind(&c.cur.bd, &c.nextStyles, 1<<2)
	c.sSides.bind(&c.cur.sides, &c.nextStyles, 1<<3)
	c.sCursor.bind(&c.cur.cursor, &c.nextStyles, 1<<4)
	c.sImage.bind(&c.cur.img, &c.nextStyles, 1<<5)
	c.sRadii.bind(&c.cur.radii, &c.nextStyles, 1<<6)
	c.sShadow.bind(&c.cur.shadow, &c.nextStyles, 1<<7)
	c.sFont.bind(&c.cur.font, &c.nextStyles, 1<<8)
	c.sAlign.bind(&c.cur.align, &c.nextStyles, 1<<9)
	c.sPad.bind(&c.cur.pad, &c.nextStyles, 1<<10)
	c.sGap.bind(&c.cur.gap, &c.nextStyles, 1<<11)
	c.sAxis.bind(&c.cur.axis, &c.nextStyles, 1<<12)
	c.sCross.bind(&c.cur.cross, &c.nextStyles, 1<<13)
	c.sJustify.bind(&c.cur.justify, &c.nextStyles, 1<<14)
	c.sW.bind(&c.cur.pref[0], &c.nextStyles, 1<<15)
	c.sH.bind(&c.cur.pref[1], &c.nextStyles, 1<<16)
	c.sOpacity.bind(&c.cur.opacity, &c.nextStyles, 1<<17)
	c.sTint.bind(&c.cur.tint, &c.nextStyles, 1<<18)
	c.styleOwner = c
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
	c.sJustify.reset()
	c.sW.reset()
	c.sH.reset()
	c.sOpacity.reset()
	c.sTint.reset()
	c.nextStyles = 0
	c.hasNextPos = false
	c.nextAnchor, c.nextInset, c.nextNudge = AnchorNone, [2]float32{}, [2]float32{}
	c.nextLo, c.nextHi, c.nextSelf = [2]float32{}, [2]float32{}, 0
	c.nextFlags = 0
	c.nextAudit = 0
	c.nextFade = 0
	c.bd.n, c.bd.hasNext = 0, false
	c.nextEscClr = false
	c.nextStill = false
	c.nextSecure = false
	c.nextCmdr, c.nextSeps = nil, ""
	c.nextOvl = OverlayOpts{}
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
	// The parent's empty-key chain is its stable anonymous ordinal. newBox
	// resumes that chain, including any other empty labels, in constant time.
	return c.Box(0, "")
}

func (c *Context) newBox(flags Flags, key Key) ID {
	flags |= c.nextFlags
	c.nextFlags = 0
	if c.nextAnchor != AnchorNone {
		flags |= floatingMask
	}
	var id ID
	found := false
	base := key
	var baseID ID
	n := uint32(0)
	if v, ok := c.table.Get(uint64(base)); ok {
		baseID = ID(v)
		b := &c.boxes[v]
		if b.lastFrame == c.frame {
			if c.dbg != nil {
				c.debugDup(base)
			}
			if b.dupLast != 0 {
				key, n = KeyN(c.boxes[b.dupLast].key, uint64(b.dupN)), b.dupN+1
			}
		} else {
			id, found = ID(v), true
		}
	}
	for !found {
		v, ok := c.table.Get(uint64(key))
		if !ok {
			break
		}
		if c.boxes[v].lastFrame != c.frame {
			id, found = ID(v), true
			break
		}
		key = KeyN(key, uint64(n)) // preserve the original derivation chain
		n++
	}
	if !found {
		id = ID(c.pool.Alloc().Index())
		if int(id) >= len(c.boxes) {
			c.boxes = append(c.boxes, box{})
		}
		c.table.Put(uint64(key), uint32(id))
		c.boxes[id].persist = persist{key: key}
	}
	if baseID != 0 && baseID != id {
		cb := &c.boxes[baseID]
		cb.dupLast, cb.dupN = id, n
	}
	b := &c.boxes[id]
	b.prevTooltip = b.tooltip && found && b.lastFrame == c.frame-1
	b.boxLinks = boxLinks{}
	b.boxAttrs = boxAttrs{}
	b.prevHid, b.hid = b.hid, false
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
	if flags&scrollMask != 0 {
		c.scrollBoxes = append(c.scrollBoxes, id)
	}
	if parent != 0 {
		b.ovl = c.boxes[parent].ovl
	}

	b.style = c.cur
	if c.nextStyles != 0 {
		c.takeNext(b)
	}
	if b.cursor != CursorArrow {
		c.nCursorBoxes++
	}
	if flags&WindowDrag != 0 {
		c.nDragBoxes++
	}
	b.auditOff, c.nextAudit = c.nextAudit, 0
	b.fadeEnd, c.nextFade = c.nextFade, 0
	c.takeBackdrop(b)
	if c.hasNextPos {
		b.fixed = c.nextPos
		c.hasNextPos = false
	}
	b.anchor, b.nudge = c.nextAnchor, c.nextNudge
	if c.nextAnchor != AnchorNone {
		b.fixed = c.nextInset
	}
	c.nextAnchor, c.nextNudge = AnchorNone, [2]float32{}
	b.lo, b.hi, b.self = c.nextLo, c.nextHi, c.nextSelf
	c.nextLo, c.nextHi, c.nextSelf = [2]float32{}, [2]float32{}, 0
	c.recordSemantics(id)
	return id
}

func (c *Context) truncationCache(b *box) *truncCache {
	if b.cut == 0 {
		i := c.cutPool.Alloc().Index()
		if int(i) == len(c.cuts) {
			c.cuts = append(c.cuts, truncCache{})
		} else {
			c.cuts[i] = truncCache{}
		}
		b.cut = i + 1
	}
	return &c.cuts[b.cut-1]
}

// takeNext applies and consumes the styles set for the next box only.
func (c *Context) takeNext(b *box) {
	pending := c.nextStyles
	c.nextStyles = 0
	b.alignNext = pending&(1<<9) != 0
	if pending&(1<<0) != 0 {
		c.sBG.takeInto(&b.bg)
	}
	if pending&(1<<1) != 0 {
		c.sText.takeInto(&b.textColor)
	}
	if pending&(1<<2) != 0 {
		c.sBorder.takeInto(&b.bd)
	}
	if pending&(1<<3) != 0 {
		c.sSides.takeInto(&b.sides)
	}
	if pending&(1<<4) != 0 {
		c.sCursor.takeInto(&b.cursor)
	}
	if pending&(1<<5) != 0 {
		c.sImage.takeInto(&b.img)
	}
	if pending&(1<<6) != 0 {
		c.sRadii.takeInto(&b.radii)
	}
	if pending&(1<<7) != 0 {
		c.sShadow.takeInto(&b.shadow)
	}
	if pending&(1<<8) != 0 {
		c.sFont.takeInto(&b.font)
	}
	if pending&(1<<9) != 0 {
		c.sAlign.takeInto(&b.align)
	}
	if pending&(1<<10) != 0 {
		c.sPad.takeInto(&b.pad)
	}
	if pending&(1<<11) != 0 {
		c.sGap.takeInto(&b.gap)
	}
	if pending&(1<<12) != 0 {
		c.sAxis.takeInto(&b.axis)
	}
	if pending&(1<<13) != 0 {
		c.sCross.takeInto(&b.cross)
	}
	if pending&(1<<14) != 0 {
		c.sJustify.takeInto(&b.justify)
	}
	if pending&(1<<15) != 0 {
		c.sW.takeInto(&b.pref[0])
	}
	if pending&(1<<16) != 0 {
		c.sH.takeInto(&b.pref[1])
	}
	if pending&(1<<17) != 0 {
		c.sOpacity.takeInto(&b.opacity)
	}
	if pending&(1<<18) != 0 {
		c.sTint.takeInto(&b.tint)
	}
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
	if c.shapeInfo != nil {
		b.run, b.textW, b.runGen = c.shapeInfo.ShapeInfo(b.font, c.fr.Cur().Bytes(sp))
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
// layout; during a build that is the previous frame's (End asks for a frame
// to settle when this frame's layout changes it, see Settling).
func (c *Context) Rect(id ID) [4]float32 {
	r := c.boxes[id].rect
	c.noteRead(id, readRect, r)
	return r
}

func rate(k, dt float32) float32 {
	if k <= 0 || dt <= 0 {
		return 0
	}
	return float32(1 - math.Exp2(float64(-k*dt)))
}
