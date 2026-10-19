package ui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// hideRig is a flow of three chips 60×10 that shows one line: at width 130 two
// of them are on it, at 100 only the first, and the second is hidden.
type hideRig struct {
	*harness
	width   float32
	flow    ID
	chips   [3]ID
	keys    [3]Key
	sigs    [3]Signal
	rtl     bool
	cursors bool
}

func newHideRig(t *testing.T) *hideRig {
	r := &hideRig{harness: newHarness(t, 600, 400), width: 130}
	r.run()
	return r
}

func (r *hideRig) build(c *Context) {
	c.NextWidth(Px(r.width))
	r.flow = c.BeginFlow("flow", FlowOpts{MaxLines: 1, RTL: r.rtl})
	for i := range r.chips {
		c.NextSize(Px(60), Px(10))
		c.NextCursor(CursorPointer)
		r.chips[i] = c.Box(Clickable|Focusable, fmt.Sprint("c", i))
		r.keys[i] = c.KeyOf(r.chips[i])
		r.sigs[i] = c.Signal(r.chips[i])
	}
	c.EndFlow()
}

func (r *hideRig) run() {
	r.frame(r.build)
	r.frame(r.build)
}

func (r *hideRig) mouseOn(i int) {
	b := r.rect(r.chips[i])
	r.move((b[0]+b[2])/2, (b[1]+b[3])/2)
}

// 1. A press held by a box that becomes hidden ends with it, and a hidden box
// reports no signal or accessibility action.
func TestFlowHiddenBoxLosesItsPressAndActions(t *testing.T) {
	r := newHideRig(t)
	if r.c.boxes[r.chips[1]].hid {
		t.Fatal("chip 1 is hidden at width 130")
	}
	r.mouseOn(1)
	r.frame(r.build)
	r.in.Down[0], r.in.Pressed[0], r.in.Clicks[0] = true, true, 1
	r.frame(r.build)
	if !r.sigs[1].Pressed {
		t.Fatal("the press did not reach chip 1")
	}
	r.frame(r.build)
	if !r.sigs[1].Dragging || r.c.ActiveCursor() != CursorPointer {
		t.Fatalf("chip 1 is not dragging: %+v cursor %v", r.sigs[1], r.c.ActiveCursor())
	}

	// the flow narrows while the button is held: chip 1 is hidden by this frame's layout
	r.width = 100
	r.frame(r.build)
	if !r.c.boxes[r.chips[1]].hid {
		t.Fatal("chip 1 is not hidden at width 100")
	}
	if r.c.ActiveKey() != 0 {
		t.Error("the press is still held by the hidden chip")
	}
	if r.c.ActiveCursor() != CursorArrow {
		t.Errorf("the drag cursor of a hidden chip is %v", r.c.ActiveCursor())
	}
	for i := 0; i < 2; i++ { // and it stays gone while the button is down, and when it is let go
		r.frame(r.build)
		if s := r.sigs[1]; s.Dragging || s.Pressed || s.Hovering {
			t.Errorf("hidden chip 1 still signals: %+v", s)
		}
	}
	r.in.Down[0], r.in.Released[0] = false, true
	r.frame(r.build)
	if s := r.sigs[1]; s.Released || s.Clicked {
		t.Errorf("hidden chip 1 got the release: %+v", s)
	}

	// accessibility requests: the visible chip gets its press, the hidden one does not
	r.in.PushAction(SemRequest{Key: r.keys[0], Action: ActPress})
	r.in.PushAction(SemRequest{Key: r.keys[1], Action: ActPress})
	r.frame(r.build)
	if !r.sigs[0].Activated || r.sigs[0].Action&ActPress == 0 {
		t.Errorf("visible chip: %+v", r.sigs[0])
	}
	if s := r.sigs[1]; s.Activated || s.Action != 0 {
		t.Errorf("hidden chip got the action: %+v", s)
	}
	r.in.PushAction(SemRequest{Key: r.keys[1], Action: ActFocus})
	r.c.Begin(&r.in, 600, 400, 1, 1.0/60) // requests for focus are applied here
	if r.c.FocusKey() == r.keys[1] {
		t.Error("an accessibility focus request reached a hidden chip")
	}
	r.c.End(&r.list)
	r.in.ClearFrame()
	r.frame(r.build)
	// the capture of a box that is hidden gives no cursor, whatever kept it
	r.c.active[0] = r.keys[1]
	if r.c.ActiveCursor() != CursorArrow {
		t.Error("ActiveCursor of a hidden box")
	}
	r.c.active[0] = r.keys[0]
	if r.c.ActiveCursor() != CursorPointer {
		t.Error("ActiveCursor of a visible box")
	}
	r.c.active[0] = 0
	// PeekSignal, as widgets use it before the box is built, sees it hidden too
	r.in.PushAction(SemRequest{Key: r.keys[1], Action: ActPress})
	r.c.Begin(&r.in, 600, 400, 1, 1.0/60)
	if s := r.c.PeekSignal(r.keys[1]); s.Activated || s.Action != 0 {
		t.Errorf("PeekSignal of a hidden chip: %+v", s)
	}
	r.c.End(&r.list)
}

// 2. Hidden boxes, and what is inside them, are left out of the pointer regions
// and of the content list.
func TestFlowHiddenBoxesHaveNoRegions(t *testing.T) {
	r := newHideRig(t)
	build := func(c *Context) {
		c.NextWidth(Px(100))
		c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		for i := 0; i < 3; i++ {
			c.NextSize(Px(60), Px(10))
			c.NextCursor(CursorPointer)
			c.Open(Clickable, fmt.Sprint("c", i))
			c.NextSize(Px(10), Px(10))
			c.NextCursor(CursorText)
			c.Box(Clickable|DrawText|WindowDrag, "")
			c.Close()
		}
		c.EndFlow()
	}
	r.frame(build)
	r.frame(build)
	cur, drag := r.c.PointerRegions(nil, nil)
	// chip 0 is visible: its own region and its inner box's; chips 1 and 2 and
	// their inner boxes are not
	if len(cur) != 2 {
		t.Errorf("%d cursor regions %v, want 2", len(cur), cur)
	}
	for _, g := range cur {
		if g.Rect[1] >= 10 || g.Rect[0] >= 60 {
			t.Errorf("a region outside the visible chip: %v", g)
		}
	}
	if len(drag) != 2 { // the visible chip blocks, its inner box drags
		t.Errorf("%d drag regions %v, want 2", len(drag), drag)
	}
	for _, g := range drag {
		if g.Rect[2] > 60 || g.Rect[3] > 10 {
			t.Errorf("a drag region outside the visible chip: %v", g)
		}
	}

	// a native list menu that a hidden box asked for is not answered
	var owner Key
	r.frame(func(c *Context) {
		c.NextWidth(Px(100))
		c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		for i := 0; i < 2; i++ {
			c.NextSize(Px(60), Px(10))
			id := c.Box(Clickable, fmt.Sprint("m", i))
			if i == 1 {
				owner = c.KeyOf(id)
			}
		}
		c.EndFlow()
	})
	opts := []string{"a", "b"}
	r.in.SetMenuChoice(owner, 1, optionsRev(opts))
	var ok bool
	r.frame(func(c *Context) {
		c.NextWidth(Px(100))
		c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		for i := 0; i < 2; i++ {
			c.NextSize(Px(60), Px(10))
			id := c.Box(Clickable, fmt.Sprint("m", i))
			if i == 1 {
				_, ok = c.MenuChoice(id, opts)
			}
		}
		c.EndFlow()
	})
	if ok {
		t.Error("the choice of a native menu reached a hidden box")
	}
}

func TestFlowHiddenContentIsNotContent(t *testing.T) {
	h := newHarness(t, 600, 400)
	build := func(c *Context) {
		c.NextWidth(Px(100))
		c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		for i := 0; i < 2; i++ {
			c.NextSize(Px(60), Px(10))
			c.Open(0, fmt.Sprint("b", i))
			c.NextSize(Px(10), Px(10)) // inside the chip, which is hidden with it
			c.NextRole(RoleButton)
			c.Box(0, "inner")
			c.Close()
		}
		c.EndFlow()
	}
	h.frame(build)
	h.frame(build)
	if got := h.c.ContentIn([4]float32{0, 0, 600, 400}, nil); len(got) != 1 {
		t.Errorf("%d content boxes, want only the visible button", len(got))
	}
}

// 3. A flow that loses all its children has no lines and no overflow, and
// asks for a frame to say so.
func TestFlowEmptiedResetsItsResults(t *testing.T) {
	h := newHarness(t, 600, 400)
	n := 5
	var flow ID
	build := func(c *Context) {
		c.NextWidth(Px(100))
		flow = c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		for i := 0; i < n; i++ {
			c.NextSize(Px(60), Px(10))
			c.Box(0, fmt.Sprint("c", i))
		}
		c.EndFlow()
	}
	for i := 0; i < 3; i++ {
		h.frame(build)
	}
	if h.c.FlowOverflow(flow) != 4 || h.c.FlowLines(flow) != 1 {
		t.Fatalf("before: overflow %d lines %d, want 4 and 1", h.c.FlowOverflow(flow), h.c.FlowLines(flow))
	}
	n = 0
	h.frame(build)
	if h.c.FlowOverflow(flow) != 0 || h.c.FlowLines(flow) != 0 {
		t.Errorf("an emptied flow reports overflow %d lines %d", h.c.FlowOverflow(flow), h.c.FlowLines(flow))
	}
	if !h.c.Animating() {
		t.Error("no frame was asked for after the results changed")
	}
	h.frame(build)
	if h.c.Animating() {
		t.Error("an empty flow keeps asking for frames")
	}
}

// 4. A scrolling RTL flow reaches the overflow of an oversize child, at its
// left, by scrolling in the reading direction.
func TestFlowRTLScroll(t *testing.T) {
	h := newHarness(t, 600, 400)
	var flow, chip, inner ID
	build := func(c *Context) {
		c.NextWidth(Px(100))
		c.NextFlags(ScrollX | Clip)
		flow = c.BeginFlow("flow", FlowOpts{RTL: true})
		c.NextSize(Px(150), Px(20))
		chip = c.Open(0, "wide")
		c.NextSize(Px(20), Px(10)) // at the chip's left end
		c.NextAxis(AxisX)
		inner = c.Box(0, "left")
		c.Close()
		c.EndFlow()
	}
	h.frame(build)
	h.frame(build)
	content, view := h.c.ScrollExtent(flow)
	if content[0] != 150 || view[0] != 100 {
		t.Fatalf("scroll extent %v in %v, want 150 in 100", content, view)
	}
	// at offset 0 the right end is in view: the chip spans -50..100
	if r := h.rect(chip); r[0] != -50 || r[2] != 100 {
		t.Fatalf("chip at %v, want -50..100", r)
	}
	// the end of the range brings the left end in view
	h.c.SetScroll(flow, 50, 0)
	h.frame(build)
	if r := h.rect(chip); r[0] != 0 || r[2] != 150 {
		t.Errorf("chip at %v after scrolling 50, want 0..150", r)
	}
	// the offset is clamped to the overflow, not to a negative range
	h.c.SetScroll(flow, 1000, 0)
	h.frame(build)
	if x, _ := h.c.Scroll(flow); x != 50 {
		t.Errorf("scroll clamped to %v, want 50", x)
	}
	h.c.SetScroll(flow, -30, 0)
	h.frame(build)
	if x, _ := h.c.Scroll(flow); x != 0 {
		t.Errorf("scroll clamped to %v, want 0", x)
	}
	if r := h.rect(chip); r[0] != -50 {
		t.Errorf("chip at %v at offset 0", r)
	}

	// an accessibility request to scroll the box at the chip's left end into
	// view moves the flow to its far end, and back for the right end
	h.in.PushAction(SemRequest{Key: h.c.KeyOf(inner), Action: ActScrollIntoView})
	h.frame(build)
	h.frame(build)
	if x := h.c.boxes[flow].scrollTgt[0]; x != 50 {
		t.Errorf("scrolling the left end into view targets %v, want 50", x)
	}
	// ScrollIntoView takes the rectangle from the right edge
	h.c.SetScroll(flow, 50, 0)
	h.frame(build)
	h.c.ScrollIntoView(flow, [4]float32{0, 0, 20, 10}) // the rightmost 20 px of the content
	h.frame(build)
	if x := h.c.boxes[flow].scrollTgt[0]; x != 0 {
		t.Errorf("scrolling the right end into view targets %v, want 0", x)
	}

	// without overflow an RTL scroll area has nothing to scroll
	h2 := newHarness(t, 600, 400)
	var f2 ID
	b2 := func(c *Context) {
		c.NextWidth(Px(100))
		c.NextFlags(ScrollX)
		f2 = c.BeginFlow("flow", FlowOpts{RTL: true})
		c.NextSize(Px(40), Px(10))
		c.Box(0, "a")
		c.EndFlow()
	}
	h2.frame(b2)
	h2.frame(b2)
	h2.c.SetScroll(f2, 30, 0)
	h2.frame(b2)
	if x, _ := h2.c.Scroll(f2); x != 0 {
		t.Errorf("scroll %v with nothing to scroll", x)
	}
}

// 5. A focused field that layout hides stops feeding the input method and the
// caret blink at once; its composition is committed in the next frame, as
// AppKit commits it when the first responder changes.
func TestFlowHidingAFocusedFieldDropsIME(t *testing.T) {
	old := macKeys
	macKeys = true
	t.Cleanup(func() { macKeys = old })
	sh := newEditShaper()
	h := &harness{t: t, c: New(sh), sh: sh.fakeShaper, w: 400, h: 100}
	h.in.Inside, h.in.Focused = true, true
	h.in.MouseX, h.in.MouseY = -100, -100
	h.c.TextEdit.Caret = 0xFF0000FF
	var buf TextBuffer
	var fieldKey Key
	now := int64(1e9)
	before := false // a wide chip first pushes the field to the second line
	var res TextFieldSignal
	frame := func() {
		now += 16e6
		h.in.Time = now
		h.frame(func(c *Context) {
			c.NextWidth(Px(100))
			c.BeginFlow("flow", FlowOpts{MaxLines: 1})
			if before {
				c.NextSize(Px(90), Px(10))
				c.Box(0, "wide")
			}
			c.NextSize(Px(60), Px(20))
			res = c.TextField("field", &buf)
			fieldKey = c.KeyOf(res.ID)
			c.EndFlow()
		})
	}
	frame()
	h.c.SetFocus(fieldKey)
	frame()
	h.in.SetPreedit([]byte("かな"), 6, 0)
	frame()
	if _, ok := h.c.IMERect(); !ok || h.c.WakeAfter() == 0 || h.c.FocusKey() != fieldKey {
		t.Fatalf("the field is not live: ime ok %v wake %d", ok, h.c.WakeAfter())
	}

	before = true // this frame's layout hides the field
	frame()
	if h.c.FocusKey() != 0 {
		t.Error("the hidden field keeps the focus")
	}
	if r, ok := h.c.IMERect(); ok || r != [4]float32{} {
		t.Errorf("the hidden field still gives the input method a rectangle: %v %v", r, ok)
	}
	if h.c.WakeAfter() != 0 {
		t.Errorf("the hidden field still arms its caret blink: %d", h.c.WakeAfter())
	}
	if buf.String() != "" {
		t.Errorf("the composition was committed early: %q", buf.String())
	}
	// the next frame commits the composition and tells the app to drop the platform's
	frame()
	if buf.String() != "かな" {
		t.Errorf("the composition of the hidden field is %q, want it committed", buf.String())
	}
	if !h.c.IMECancel() {
		t.Error("the platform's composition is not discarded")
	}
	if _, ok := h.c.IMERect(); ok || h.c.WakeAfter() != 0 {
		t.Error("the hidden field is live again")
	}
	h.in.SetPreedit(nil, 0, 0)
	frame()
	if buf.String() != "かな" || h.c.IMECancel() {
		t.Errorf("after the platform's answer: %q cancel %v", buf.String(), h.c.IMECancel())
	}
	// an app that gives a hidden field the focus does not make it a live one
	h.c.SetFocus(fieldKey)
	frame()
	if res.Focused {
		t.Error("a hidden field is built as focused")
	}
	if _, ok := h.c.IMERect(); ok {
		t.Error("a hidden field the app focused gave the input method a rectangle")
	}
}

// indRig builds a flow of chips and the overflow indicator, labelled from the
// previous layout's FlowOverflow as an app would.
type indRig struct {
	*harness
	width  float32
	opts   FlowOpts
	w      []float32
	indW   float32
	flow   ID
	chips  []ID
	ind    ID
	label  string // the indicator's label as built
	hidden []bool // which chips are hidden in the last layout
}

func (r *indRig) build(c *Context) {
	c.NextWidth(Px(r.width))
	r.flow = c.BeginFlow("flow", r.opts)
	r.chips = r.chips[:0]
	for i, w := range r.w {
		c.NextSize(Px(w), Px(10))
		r.chips = append(r.chips, c.Box(0, fmt.Sprint("c", i)))
	}
	r.label = fmt.Sprint("+", c.FlowOverflow(r.flow))
	c.NextFlowOverflow(r.indW)
	c.NextHeight(Px(14))
	r.ind = c.Box(DrawText, r.label+"##ind")
	c.EndFlow()
}

// frame builds one frame and reads the layout back.
func (r *indRig) frame1() {
	r.harness.frame(r.build)
	r.hidden = r.hidden[:0]
	for _, id := range r.chips {
		r.hidden = append(r.hidden, r.c.boxes[id].hid)
	}
}

func (r *indRig) shown() string {
	var sb strings.Builder
	for i, h := range r.hidden {
		if !h {
			sb.WriteString(fmt.Sprint(i, " "))
		}
	}
	if !r.c.boxes[r.ind].hid {
		sb.WriteString("+")
	}
	return strings.TrimSpace(sb.String())
}

// 6. The overflow indicator: the reviewer's example (three 40 px tags in 100
// px on one line) with the indicator built from the count.
func TestFlowOverflowIndicator(t *testing.T) {
	r := &indRig{harness: newHarness(t, 600, 400), width: 100, opts: FlowOpts{MaxLines: 1}, w: []float32{40, 40, 40}, indW: 30}
	r.frame1()
	// the layout is final in the first frame: one tag and the indicator
	if got := r.shown(); got != "0 +" {
		t.Fatalf("frame 1 shows %q, want the first tag and the indicator", got)
	}
	if b := r.rect(r.ind); b[0] != 40 || b[2]-b[0] != 30 || b[3]-b[1] != 14 {
		t.Errorf("indicator at %v", b)
	}
	if r.label != "+0" { // the label lags: no count yet
		t.Errorf("frame 1 label %q", r.label)
	}
	if !r.c.Animating() {
		t.Error("the changed count did not ask for the frame that shows it")
	}
	r.frame1()
	if r.label != "+2" || r.shown() != "0 +" {
		t.Errorf("frame 2: label %q shows %q, want +2 and the same layout", r.label, r.shown())
	}
	if r.c.Animating() {
		t.Error("frame 2 still asks for frames")
	}
	if r.c.FlowOverflow(r.flow) != 2 {
		t.Errorf("overflow %d, indicator not counted, want 2", r.c.FlowOverflow(r.flow))
	}
	// the flow is as high as the indicator, the tallest in its line
	if b := r.rect(r.flow); b[3]-b[1] != 14 {
		t.Errorf("flow height %v, want 14", b[3]-b[1])
	}
	for i := 0; i < 3; i++ {
		r.frame1()
		if r.label != "+2" || r.shown() != "0 +" || r.c.Animating() {
			t.Fatalf("frame %d: label %q shows %q animating %v", i+3, r.label, r.shown(), r.c.Animating())
		}
	}

	// no overflow: the indicator is hidden and takes no room
	r.w = []float32{40, 40}
	r.frame1()
	if got := r.shown(); got != "0 1" {
		t.Errorf("without overflow the layout is %q", got)
	}
	if b := r.rect(r.flow); b[3]-b[1] != 10 {
		t.Errorf("flow height %v without the indicator, want 10", b[3]-b[1])
	}
	r.frame1()
	r.frame1()
	if r.label != "+0" || r.c.Animating() {
		t.Errorf("label %q animating %v", r.label, r.c.Animating())
	}
	// two chips of 40 and the indicator would not fit one line of 100 with a
	// third chip, but with exactly the room they do
	r.w = []float32{35, 35}
	r.indW = 30
	r.frame1()
	if got := r.shown(); got != "0 1" {
		t.Errorf("two chips that fit without the indicator: %q", got)
	}
	// 3 chips: [35 35] fit with the indicator exactly (35+35+30 = 100)
	r.w = []float32{35, 35, 35}
	r.frame1()
	if got := r.shown(); got != "0 1 +" {
		t.Errorf("exact fit with the indicator: %q", got)
	}
	// one pixel more and the second chip gives its place
	r.indW = 31
	r.frame1()
	if got := r.shown(); got != "0 +" {
		t.Errorf("one pixel short: %q", got)
	}
	// an indicator wider than the flow is alone
	r.indW = 130
	r.frame1()
	if got := r.shown(); got != "+" {
		t.Errorf("a wide indicator: %q", got)
	}
}

func TestFlowOverflowIndicatorLinesAndDirection(t *testing.T) {
	// lines: [40 40] [30 30 30] [30]; two lines are shown and the indicator is on the second
	for _, c := range []struct {
		name string
		opts FlowOpts
		want map[string]float32 // x of chip 2, chip 3 and the indicator
	}{
		{"ltr", FlowOpts{MaxLines: 2}, map[string]float32{"c2": 0, "c3": 30, "ind": 60}},
		{"rtl", FlowOpts{MaxLines: 2, RTL: true}, map[string]float32{"c2": 70, "c3": 40, "ind": 0}},
		{"center", FlowOpts{MaxLines: 2, Align: FlowCenter}, map[string]float32{"c2": 0, "c3": 30, "ind": 60}},
		{"end", FlowOpts{MaxLines: 2, Align: FlowEnd}, map[string]float32{"c2": 0, "c3": 30, "ind": 60}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &indRig{harness: newHarness(t, 600, 400), width: 100, opts: c.opts, w: []float32{40, 40, 30, 30, 30, 30}, indW: 40}
			r.frame1()
			// line 2: 30, 30, then the indicator (40): 30+30+40 = 100; the third 30 is hidden
			if got := r.shown(); got != "0 1 2 3 +" {
				t.Fatalf("shows %q", got)
			}
			x := func(id ID) float32 { return r.rect(id)[0] }
			got := map[string]float32{"c2": x(r.chips[2]), "c3": x(r.chips[3]), "ind": x(r.ind)}
			// the line is 100 wide, so it fills the flow whatever the alignment
			for k, w := range c.want {
				if !near(got[k], w) {
					t.Errorf("%s at x=%v, want %v", k, got[k], w)
				}
			}
			if y := r.rect(r.ind)[1]; y != 10 { // on the second line, below the first (10 high)
				t.Errorf("indicator at y=%v, want the second line at 10", y)
			}
		})
	}
}

// The indicator makes layout independent of its label: for any construction
// the layout of the second frame equals the first's, the label is right in the
// second, and the third frame asks for nothing.
func TestFlowOverflowIndicatorSettlesWithinTwoFrames(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	type snap struct {
		shown string
		rects [][4]float32
		over  int
		lines int
	}
	take := func(r *indRig) snap {
		s := snap{shown: r.shown(), over: r.c.FlowOverflow(r.flow), lines: r.c.FlowLines(r.flow)}
		for _, id := range r.chips {
			s.rects = append(s.rects, r.rect(id))
		}
		s.rects = append(s.rects, r.rect(r.ind))
		return s
	}
	same := func(a, b snap) bool {
		if a.shown != b.shown || a.over != b.over || a.lines != b.lines || len(a.rects) != len(b.rects) {
			return false
		}
		for i := range a.rects {
			if a.rects[i] != b.rects[i] {
				return false
			}
		}
		return true
	}
	cases := 0
	for n := 0; n < 400; n++ {
		count := 1 + rng.Intn(14)
		w := make([]float32, count)
		for i := range w {
			w[i] = float32(10 + rng.Intn(70))
		}
		r := &indRig{
			harness: newHarness(t, 600, 400),
			width:   float32(40 + rng.Intn(160)),
			w:       w,
			indW:    float32(20 + rng.Intn(50)),
			opts: FlowOpts{
				GapX: float32(rng.Intn(8)), GapY: float32(rng.Intn(6)), MaxLines: 1 + rng.Intn(3),
				Align: FlowAlign(rng.Intn(4)), Cross: Cross(rng.Intn(3)), RTL: rng.Intn(2) == 0,
			},
		}
		if n == 0 { // the reviewer's example
			r.width, r.w, r.indW, r.opts = 100, []float32{40, 40, 40}, 30, FlowOpts{MaxLines: 1}
		}
		r.frame1()
		first := take(r)
		r.frame1()
		second := take(r)
		if !same(first, second) {
			t.Fatalf("case %d %+v width %v chips %v ind %v: the layout changed from frame 1 to 2:\n%+v\n%+v", n, r.opts, r.width, r.w, r.indW, first, second)
		}
		if want := fmt.Sprint("+", r.c.FlowOverflow(r.flow)); r.label != want {
			t.Fatalf("case %d: label %q in frame 2, want %q", n, r.label, want)
		}
		for i := 0; i < 3; i++ {
			r.frame1()
			if !same(first, take(r)) {
				t.Fatalf("case %d: the layout moved in frame %d", n, i+3)
			}
			if r.c.Animating() {
				t.Fatalf("case %d: frame %d still asks for frames", n, i+3)
			}
		}
		// the indicator is only there when children are hidden, and never overlaps
		hid := 0
		for _, h := range r.hidden {
			if h {
				hid++
			}
		}
		if (hid > 0) == r.c.boxes[r.ind].hid {
			t.Fatalf("case %d: %d chips hidden but the indicator hidden=%v", n, hid, r.c.boxes[r.ind].hid)
		}
		if hid != r.c.FlowOverflow(r.flow) {
			t.Fatalf("case %d: %d hidden, overflow %d", n, hid, r.c.FlowOverflow(r.flow))
		}
		if hid > 0 {
			cases++
			ir := r.rect(r.ind)
			for i, id := range r.chips {
				if cr := r.rect(id); !r.hidden[i] && cr[0] < ir[2] && ir[0] < cr[2] && cr[1] < ir[3] && ir[1] < cr[3] {
					t.Fatalf("case %d: chip %d %v overlaps the indicator %v", n, i, cr, ir)
				}
			}
			if ir[2]-ir[0] != r.indW {
				t.Fatalf("case %d: the indicator is %v wide, want %v", n, ir[2]-ir[0], r.indW)
			}
		}
	}
	if cases < 100 {
		t.Errorf("only %d of the constructions overflowed: the test does not cover enough", cases)
	}
}

// A flow without MaxLines, or an indicator that is not the last child, is not
// an indicator: it is an ordinary child.
func TestFlowOverflowIndicatorNeedsMaxLinesAndLastPlace(t *testing.T) {
	r := &indRig{harness: newHarness(t, 600, 400), width: 100, opts: FlowOpts{}, w: []float32{40, 40, 40}, indW: 30}
	r.frame1()
	if r.c.boxes[r.ind].hid || r.c.FlowOverflow(r.flow) != 0 {
		t.Error("an unlimited flow hid its indicator or its children")
	}
	h := newHarness(t, 600, 400)
	var mid ID
	h.frame(func(c *Context) {
		c.NextWidth(Px(100))
		c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		c.NextFlowOverflow(30)
		mid = c.Box(0, "notlast")
		for i := 0; i < 3; i++ {
			c.NextSize(Px(40), Px(10))
			c.Box(0, fmt.Sprint("c", i))
		}
		c.EndFlow()
	})
	if h.c.boxes[mid].hid {
		t.Error("a marked box that is not last was treated as the indicator")
	}
}

func TestFlowIndicatorAllocatesNothing(t *testing.T) {
	r := &indRig{harness: newHarness(t, 600, 400), width: 100, opts: FlowOpts{MaxLines: 1, GapX: 3}, w: []float32{40, 40, 40}, indW: 30}
	r.indW = 30
	build := func(c *Context) {
		c.NextWidth(Px(100))
		f := c.BeginFlow("flow", FlowOpts{MaxLines: 2, GapX: 3, GapY: 2})
		for i := 0; i < 12; i++ {
			c.NextSize(Px(40), Px(10))
			c.Box(0, fmt.Sprint("c", i))
		}
		_ = f
		c.NextFlowOverflow(30)
		c.Box(0, "more")
		c.EndFlow()
	}
	for i := 0; i < 5; i++ {
		r.harness.frame(build)
	}
	// build allocates for fmt.Sprint: measure the layout only
	if n := testing.AllocsPerRun(50, func() { r.c.layout() }); n != 0 {
		t.Errorf("layout allocates %v", n)
	}
}

// An accessibility request to scroll a hidden box into view moves nothing.
func TestFlowHiddenBoxIsNotScrolledTo(t *testing.T) {
	h := newHarness(t, 600, 400)
	var area ID
	var keys [4]Key
	build := func(c *Context) {
		c.NextSize(Px(100), Px(40))
		area = c.Open(ScrollY|Clip, "area")
		c.NextWidth(Px(100))
		c.BeginFlow("flow", FlowOpts{MaxLines: 4})
		for i := range keys {
			c.NextSize(Px(100), Px(30))
			keys[i] = c.KeyOf(c.Box(0, fmt.Sprint("c", i)))
		}
		for i := 0; i < 3; i++ { // lines 5 to 7: hidden
			c.NextSize(Px(100), Px(30))
			keys[3] = c.KeyOf(c.Box(0, fmt.Sprint("h", i)))
		}
		c.EndFlow()
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	h.c.SetScroll(area, 0, 60)
	h.frame(build)
	if _, y := h.c.Scroll(area); y != 60 {
		t.Fatalf("scroll %v, want 60", y)
	}
	h.in.PushAction(SemRequest{Key: keys[3], Action: ActScrollIntoView}) // the last hidden box
	h.frame(build)
	if y := h.c.boxes[area].scrollTgt[1]; y != 60 {
		t.Errorf("a hidden box scrolled the area to %v", y)
	}
}

// A link of a RichText box that a flow hides takes no accessibility press.
func TestFlowHiddenRichTextLinkIsInert(t *testing.T) {
	body := "go to example now"
	r := newRich(t, body, []Span{spanOf(body, "example", 1, SpanLink, 42)}, RichOpts{})
	width := float32(400)
	build := func(c *Context) {
		c.NextWidth(Px(width))
		c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		c.NextSize(Px(90), Px(14))
		c.Box(0, "first")
		r.o.MaxW = 150
		r.build(c)
		c.EndFlow()
	}
	r.frame(build)
	r.frame(build)
	ln, ok := linkNode(r.c)
	if !ok {
		t.Fatal("no link node while visible")
	}
	r.in.PushAction(SemRequest{Key: ln.Key, Action: ActPress})
	r.frame(build)
	if r.sig.Clicked != 42 {
		t.Fatalf("a visible link ignored the press: %d", r.sig.Clicked)
	}
	width = 100 // the rich text no longer fits on the line: hidden
	r.frame(build)
	r.in.PushAction(SemRequest{Key: ln.Key, Action: ActPress})
	r.frame(build)
	if !r.c.boxes[r.sig.ID].hid {
		t.Fatal("the rich text is not hidden")
	}
	if r.sig.Clicked != 0 {
		t.Errorf("a hidden link was clicked: %d", r.sig.Clicked)
	}
}
