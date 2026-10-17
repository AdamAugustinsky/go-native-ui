package rex

import (
	"math"
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"
)

// These tests drive the scene over a fake text system (no fonts, no GPU), so
// they run the same on every OS.

// ---- geometry helpers ----

// cursor is the cursor the window would show at the pointer after the last
// frame: that of the box holding the mouse button, else of the topmost cursor
// region under the pointer.
func (h *hrn) cursor() ui.Cursor {
	if c := h.c.ActiveCursor(); c != ui.CursorArrow {
		return c
	}
	regs := h.c.CursorRegions(nil)
	for i := len(regs) - 1; i >= 0; i-- {
		r := regs[i].Rect
		if h.in.MouseX >= r[0] && h.in.MouseX < r[2] && h.in.MouseY >= r[1] && h.in.MouseY < r[3] {
			return regs[i].Cursor
		}
	}
	return ui.CursorArrow
}

func (h *hrn) tabRect(i int) [4]float32 { return h.x.tabRects[i] }

// paneSlot returns the slot of the pane of the given kind in the current tab.
func (h *hrn) paneSlot(kind paneKind) int {
	t := h.cur()
	for i := range t.panes {
		if t.panes[i].used && t.panes[i].kind == kind {
			return i
		}
	}
	h.t.Fatalf("no pane of kind %d", kind)
	return -1
}

func (h *hrn) rectOf(slot int) [4]float32 { return h.x.disp[slot] }

// btn is the centre of header button b of the pane in slot.
func (h *hrn) btn(slot, b int) (float32, float32) {
	d := h.rectOf(slot)
	k := h.x.k
	dx := [numBtns]float32{181.5, 133.5, 83, 37.5}
	return d[2] - dx[b]*k, d[1] + 36.5*k + k
}

// blank is a point inside the pane that holds no text and no control.
func (h *hrn) blank(slot int) (float32, float32) {
	d := h.rectOf(slot)
	return d[2] - 30*h.x.k, d[3] - 30*h.x.k
}

func (h *hrn) rootDivider() (rect [4]float32, n int8) {
	t := h.cur()
	n = t.root
	return t.nodes[n].gut, n
}

func inside(r, outer [4]float32) bool {
	return r[0] >= outer[0] && r[1] >= outer[1] && r[2] <= outer[2] && r[3] <= outer[3]
}

func overlap(a, b [4]float32) bool {
	return a[0] < b[2] && b[0] < a[2] && a[1] < b[3] && b[1] < a[3]
}

func snapRef(v float64) float64 { return math.Floor(v + 0.5) }

// ---- layout ----

// The initial layout is the reference's: margins and gutters, and the split
// ratios measured on the screenshot.
func TestInitialLayout(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	if h.w != 2114 || h.h != 1278 {
		t.Fatalf("window %dx%d", h.w, h.h)
	}
	agent, git, snake := h.rectOf(h.paneSlot(kindAgent)), h.rectOf(h.paneSlot(kindGit)), h.rectOf(h.paneSlot(kindSnake))
	x1 := snapRef(15 + (2114-30-14)*0.54638)
	y1 := snapRef(91 + (1278-91-15-15)*0.43302)
	for _, c := range []struct {
		name      string
		got, want [4]float32
	}{
		{"agent", agent, [4]float32{15, 91, float32(x1), float32(y1)}},
		{"git", git, [4]float32{15, float32(y1) + 15, float32(x1), 1278 - 15}},
		{"snake", snake, [4]float32{float32(x1) + 14, 91, 2114 - 15, 1278 - 15}},
	} {
		if c.got != c.want {
			t.Errorf("%s pane = %v, want %v", c.name, c.got, c.want)
		}
	}
	if h.x.active != 0 || h.x.nTabs != 3 || h.cur().numPanes() != 3 {
		t.Errorf("initial state: tab %d of %d, %d panes", h.x.active, h.x.nTabs, h.cur().numPanes())
	}
	if h.cur().focus != h.cur().panes[h.paneSlot(kindAgent)].uid {
		t.Error("the agent pane starts focused")
	}
	if h.c.Animating() {
		t.Error("the initial state animates")
	}
}

// At every window size from 600×400 to 3000×1800 pt, on 1×, 2× and 3×
// displays, the panes stay inside the work area, do not overlap and keep the
// gutters; the layout is the same frame a resize happens (nothing glides).
func TestLayoutAnySize(t *testing.T) {
	for _, scale := range []float32{1, 2, 3} {
		h := newHarness(t, 1057, 639, scale)
		for _, sz := range [][2]int{{600, 400}, {700, 450}, {1057, 639}, {1600, 1000}, {3000, 1800}, {900, 1200}, {600, 400}} {
			h.w, h.h = int(float32(sz[0])*scale), int(float32(sz[1])*scale)
			h.frame()
			if h.c.Animating() {
				t.Errorf("%v@%v: animating on the resize frame", sz, scale)
			}
			h.checkLayout(sz[0], sz[1], scale)
		}
	}
}

func (h *hrn) checkLayout(wPt, hPt int, scale float32) {
	h.t.Helper()
	work := h.x.work()
	gt := h.x.gutters()
	tab := h.cur()
	var rects [][4]float32
	for i := range tab.panes {
		if !tab.panes[i].used {
			continue
		}
		r := h.rectOf(i)
		if tab.rects[i] != r {
			h.t.Errorf("%dx%dpt@%v: pane %d displayed at %v, laid out at %v (stale during resize)", wPt, hPt, scale, i, r, tab.rects[i])
		}
		if !inside(r, work) || r[2]-r[0] < 1 || r[3]-r[1] < 1 {
			h.t.Errorf("%dx%dpt@%v: pane %d at %v outside the work area %v", wPt, hPt, scale, i, r, work)
		}
		for _, o := range rects {
			if overlap(r, o) {
				h.t.Errorf("%dx%dpt@%v: panes overlap: %v %v", wPt, hPt, scale, r, o)
			}
		}
		rects = append(rects, r)
	}
	// Every split keeps its gutter.
	for i := range tab.nodes {
		n := &tab.nodes[i]
		if !n.used || n.leaf {
			continue
		}
		g := n.gut
		want := gt.x
		got := g[2] - g[0]
		if n.axis == ui.AxisY {
			want, got = gt.y, g[3]-g[1]
		}
		if got != want {
			h.t.Errorf("%dx%dpt@%v: gutter %v px, want %v", wPt, hPt, scale, got, want)
		}
	}
	if len(rects) > 1 {
		// Panes use the work area completely: the union's bounds are the work area.
		u := rects[0]
		for _, r := range rects[1:] {
			u = [4]float32{min(u[0], r[0]), min(u[1], r[1]), max(u[2], r[2]), max(u[3], r[3])}
		}
		if u != work {
			h.t.Errorf("%dx%dpt@%v: panes cover %v, work area %v", wPt, hPt, scale, u, work)
		}
	}
}

// A window too small for every pane's minimum size still lays out inside the
// work area without overlaps (the minimums give way proportionally).
func TestLayoutCrowded(t *testing.T) {
	h := newHarness(t, 600, 400, 2)
	for i := 0; i < 12; i++ {
		tab := h.cur()
		slot := tab.slotOf(tab.focus)
		axis := ui.AxisX
		if i%2 == 1 {
			axis = ui.AxisY
		}
		h.x.splitFocus(tab, slot, axis)
		h.settle()
		h.checkLayout(600, 400, 2)
	}
	if n := h.cur().numPanes(); n != maxPanes {
		t.Errorf("%d panes, want the limit %d", n, maxPanes)
	}
}

// splitFocus is the split button as a method for tests of the model alone.
func (r *Rex) splitFocus(t *tab, slot int8, axis ui.Axis) {
	b := btnSplitX
	if axis == ui.AxisY {
		b = btnSplitY
	}
	r.button(t, slot, b)
}

// ---- tree ----

func TestTreeSplitAndClose(t *testing.T) {
	var tb tab
	tb.setRoot(1, kindShell, 100)
	if tb.closeLeaf(0) {
		t.Fatal("the last pane cannot be closed")
	}
	np := tb.splitLeaf(0, ui.AxisX, 0.5, 2, kindGit, 101, 102)
	if np < 0 || tb.numPanes() != 2 {
		t.Fatal("split failed")
	}
	g := &gutters{x: 10, y: 10, minW: 100, minH: 50}
	tb.layout([4]float32{0, 0, 410, 200}, g)
	if tb.rects[0] != [4]float32{0, 0, 200, 200} || tb.rects[np] != [4]float32{210, 0, 410, 200} {
		t.Errorf("halves: %v %v", tb.rects[0], tb.rects[np])
	}
	// Splitting the second pane stacks it inside its half.
	nn := tb.splitLeaf(np, ui.AxisY, 0.5, 3, kindSnake, 103, 104)
	tb.layout([4]float32{0, 0, 410, 200}, g)
	if tb.rects[np] != [4]float32{210, 0, 410, 95} || tb.rects[nn] != [4]float32{210, 105, 410, 200} {
		t.Errorf("stacked: %v %v", tb.rects[np], tb.rects[nn])
	}
	// Closing the first pane gives its space to the other column.
	tb.focus = 1
	if !tb.closeLeaf(0) {
		t.Fatal("close failed")
	}
	tb.layout([4]float32{0, 0, 410, 200}, g)
	if tb.rects[np] != [4]float32{0, 0, 410, 95} || tb.rects[nn] != [4]float32{0, 105, 410, 200} {
		t.Errorf("after close: %v %v", tb.rects[np], tb.rects[nn])
	}
	if tb.focus != tb.panes[np].uid {
		t.Errorf("focus %d after closing the focused pane, want its sibling %d", tb.focus, tb.panes[np].uid)
	}
	// The tab holds at most maxPanes.
	for tb.numPanes() < maxPanes {
		if tb.splitLeaf(np, ui.AxisX, 0.5, uint32(10+tb.numPanes()), kindShell, uint32(200+tb.numPanes()), uint32(300+tb.numPanes())) < 0 {
			t.Fatal("split below the limit failed")
		}
	}
	if tb.splitLeaf(np, ui.AxisX, 0.5, 99, kindShell, 98, 97) >= 0 {
		t.Error("split beyond the limit succeeded")
	}
}

func TestDragSplitRespectsMinimums(t *testing.T) {
	var tb tab
	tb.setRoot(1, kindShell, 100)
	tb.splitLeaf(0, ui.AxisX, 0.5, 2, kindGit, 101, 102)
	g := &gutters{x: 10, y: 10, minW: 100, minH: 50}
	work := [4]float32{0, 0, 410, 200}
	tb.layout(work, g)
	root := tb.root
	tb.nodes[root].press = tb.nodes[root].ratio
	tb.dragSplit(root, 37)
	tb.layout(work, g)
	if got := tb.rects[0][2]; got != 237 {
		t.Errorf("dragged by 37: first pane ends at %v, want 237", got)
	}
	tb.dragSplit(root, -5000)
	tb.layout(work, g)
	if got := tb.rects[0][2]; got != 100 {
		t.Errorf("dragged far left: %v, want the minimum 100", got)
	}
	tb.dragSplit(root, 5000)
	tb.layout(work, g)
	if got := tb.rects[1][2] - tb.rects[1][0]; got != 100 {
		t.Errorf("dragged far right: second pane %v wide, want the minimum 100", got)
	}
}

// ---- interaction ----

func TestHoverTab(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	rc := h.tabRect(1)
	hasFill := func() bool {
		for _, q := range h.list.Quads {
			if q.Rect == rc && q.Color[0]&255 != 0 && q.Mode == gpu.ModeShape {
				return true
			}
		}
		return false
	}
	if hasFill() {
		t.Fatal("an unhovered tab has a fill")
	}
	cx, cy := centre(rc)
	h.move(cx, cy)
	h.frame()
	h.frame()
	if !h.c.Animating() {
		t.Error("hover does not animate")
	}
	if !hasFill() {
		t.Error("a hovered inactive tab shows no hover fill")
	}
	if got := h.cursor(); got != ui.CursorPointer {
		t.Errorf("cursor over a tab = %v, want the pointer", got)
	}
	h.settle()
	if !hasFill() || h.list.Quads == nil {
		t.Error("hover fill lost when settled")
	}
	// The active tab shows no hover fill beyond the pill.
	h.move(centre(h.tabRect(0)))
	h.settle()
	for _, q := range h.list.Quads {
		if q.Rect == h.tabRect(0) && q.Color[0]&^255 == 0xFFFFFF00 { // white translucent: the hover fill (the pill is FFFAFF)
			t.Error("the active tab got a hover fill")
		}
	}
	h.move(5, 700)
	h.settle()
	if hasFill() || h.cursor() != ui.CursorArrow {
		t.Error("hover state stays after the pointer left")
	}
}

func TestClickTabAnimatesPill(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	from := h.x.pill
	if from[0] != h.tabRect(0)[0] || from[2] != h.tabRect(0)[2] {
		t.Fatalf("the pill starts at %v, tab 0 is %v", from, h.tabRect(0))
	}
	tab3 := h.tabRect(2)
	cx, cy := centre(tab3)
	h.move(cx, cy)
	h.frame()
	h.in.SetButton(ui.ButtonLeft, true, 1)
	h.frame()
	h.in.SetButton(ui.ButtonLeft, false, 0)
	h.frame()
	if h.x.active != 2 {
		t.Fatalf("active tab %d after clicking tab 3", h.x.active)
	}
	// Frame by frame the pill moves monotonically toward the new tab and arrives.
	prev := h.x.pill[0]
	sawMid := false
	for i := 0; i < 200 && h.c.Animating(); i++ {
		h.frame()
		p := h.x.pill[0]
		if p < prev {
			t.Fatalf("pill moved backwards: %v → %v", prev, p)
		}
		if p > from[0]+4 && p < tab3[0]-4 {
			sawMid = true
		}
		prev = p
	}
	if !sawMid {
		t.Error("the pill jumped instead of sliding")
	}
	if h.c.Animating() {
		t.Fatal("tab switch never settled")
	}
	if h.x.pill[0] != tab3[0] || h.x.pill[2] != tab3[2] {
		t.Errorf("pill settled at %v, tab 3 is %v", h.x.pill, tab3)
	}
	if h.cur().numPanes() != 1 {
		t.Errorf("tab 3 shows %d panes", h.cur().numPanes())
	}
	// And back.
	h.click(centre(h.tabRect(0)))
	h.settle()
	if h.x.active != 0 || h.x.pill[0] != h.tabRect(0)[0] {
		t.Errorf("back on tab 1: active %d, pill %v", h.x.active, h.x.pill)
	}
}

func TestNewTabAndCloseTab(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	W := float32(h.w)
	plus := [2]float32{W - 46.5*h.x.k, 45 * h.x.k}
	h.click(plus[0], plus[1])
	h.settle()
	if h.x.nTabs != 4 || h.x.active != 3 {
		t.Fatalf("after +: %d tabs, active %d", h.x.nTabs, h.x.active)
	}
	// The tabs share the capsule: all the same width.
	w0 := h.tabRect(0)[2] - h.tabRect(0)[0]
	for i := 1; i < 4; i++ {
		if d := (h.tabRect(i)[2] - h.tabRect(i)[0]) - w0; d < -1 || d > 1 {
			t.Errorf("tab %d is %v px wider than tab 0", i, d)
		}
	}
	// Close it with its only pane's close button: the tab goes.
	slot := h.paneSlot(kindShell)
	h.click(h.btn(slot, btnClose))
	h.settle()
	if h.x.nTabs != 3 || h.x.active != 2 {
		t.Errorf("after closing the new tab: %d tabs, active %d", h.x.nTabs, h.x.active)
	}
	// The last pane of the last tab cannot be closed.
	for h.x.nTabs > 1 {
		h.x.closeTab(h.x.nTabs - 1)
		h.settle()
	}
	if h.x.canClose() && h.cur().numPanes() == 1 {
		t.Error("the last pane of the last tab can be closed")
	}
}

func TestFocusSwitchAnimatesDimming(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	agentSlot, snakeSlot := h.paneSlot(kindAgent), h.paneSlot(kindSnake)
	snake := h.rectOf(snakeSlot)
	// First mesh cell of a pane: its colour is the dimmed fill until the pane is focused.
	cell := func(r [4]float32) (uint32, bool) {
		for _, q := range h.list.Quads {
			if q.Rect[0] == r[0] && q.Rect[1] == r[1] && q.Mode == gpu.ModeShape && q.Radii[0] > 0 && q.Border == 0 && q.Soft == 0 {
				return q.Color[3], true
			}
		}
		return 0, false
	}
	before, ok := cell(snake)
	if !ok || before == 0xFFFFFFFF {
		t.Fatalf("the unfocused snake pane should draw a tinted fill cell: %08x %v", before, ok)
	}
	bx, by := h.blank(snakeSlot)
	h.move(bx, by)
	h.frame()
	h.in.SetButton(ui.ButtonLeft, true, 1)
	h.frame()
	h.in.SetButton(ui.ButtonLeft, false, 0)
	if h.cur().focus != h.cur().panes[snakeSlot].uid {
		t.Fatal("pressing a pane does not focus it")
	}
	h.frame()
	if !h.c.Animating() {
		t.Fatal("the dimming does not animate")
	}
	// Brightness of the fill cell rises monotonically to white (blue channel of an unfocused fill is the lowest).
	var last uint32
	n := 0
	for h.c.Animating() && n < 400 {
		h.frame()
		c, ok := cell(snake)
		if !ok {
			break // settled: a plain white box replaces the mesh
		}
		if b := (c >> 8) & 255; b < last {
			t.Fatalf("fill got darker while focusing: %d → %d", last, b)
		} else {
			last = b
		}
		n++
	}
	if n < 5 {
		t.Errorf("dimming took %d frames", n)
	}
	h.settle()
	white := false
	for _, q := range h.list.Quads {
		if q.Rect == snake && q.Color[0] == 0xFFFFFFFF && q.Radii[0] > 0 {
			white = true
		}
	}
	if !white {
		t.Error("focused pane is not an opaque white card")
	}
	if _, ok := cell(h.rectOf(agentSlot)); !ok {
		t.Error("the agent pane did not dim")
	}
	if h.c.Animating() {
		t.Error("still animating")
	}
}

func TestDragVerticalDivider(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	g, n := h.rootDivider()
	cx, cy := (g[0]+g[2])/2, (g[1]+g[3])/2
	agent, snake := h.paneSlot(kindAgent), h.paneSlot(kindSnake)
	x1 := h.rectOf(agent)[2]

	h.move(cx, cy)
	h.frame()
	h.frame()
	if h.cursor() != ui.CursorResizeEW {
		t.Errorf("cursor over the vertical divider = %v", h.cursor())
	}
	h.in.SetButton(ui.ButtonLeft, true, 1)
	h.frame()
	// Drag right in steps: the divider follows the pointer exactly (±1 px), the same frame.
	for _, dx := range []float32{10, 40, 120, 200, 120, 40, 0, -60, -150} {
		h.move(cx+dx, cy+300) // the pointer leaves the divider vertically: the drag holds
		h.frame()
		if got := h.rectOf(agent)[2] - x1; math.Abs(float64(got-dx)) > 1 {
			t.Errorf("dragged by %v: divider moved %v", dx, got)
		}
		if h.cursor() != ui.CursorResizeEW {
			t.Errorf("cursor while dragging = %v", h.cursor())
		}
		if got := h.rectOf(snake)[0] - h.rectOf(agent)[2]; got != 14 {
			t.Errorf("gutter %v while dragging", got)
		}
		if h.c.Animating() && dx == 0 {
			// the handle fades in; nothing may glide
		}
	}
	h.in.SetButton(ui.ButtonLeft, false, 0)
	h.frame()
	h.settle()
	want := h.rectOf(agent)[2]

	// Dragging to either extreme keeps the minimum width.
	gt := h.x.gutters()
	g2, _ := h.rootDivider()
	h.move((g2[0]+g2[2])/2, cy)
	h.frame()
	h.in.SetButton(ui.ButtonLeft, true, 1)
	h.frame()
	h.move(-500, cy)
	h.frame()
	if w := h.rectOf(agent)[2] - h.rectOf(agent)[0]; w < gt.minW-1 {
		t.Errorf("left pane %v px wide, minimum %v", w, gt.minW)
	}
	h.move(10000, cy)
	h.frame()
	if w := h.rectOf(snake)[2] - h.rectOf(snake)[0]; w < gt.minW-1 {
		t.Errorf("right pane %v px wide, minimum %v", w, gt.minW)
	}
	h.in.SetButton(ui.ButtonLeft, false, 0)
	h.frame()
	h.settle()
	_ = want
	_ = n
	h.checkLayout(WindowWidthPt, WindowHeightPt, 2)
}

func TestDragHorizontalDivider(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	tab := h.cur()
	var n int8 = -1
	for i := range tab.nodes {
		if tab.nodes[i].used && !tab.nodes[i].leaf && tab.nodes[i].axis == ui.AxisY {
			n = int8(i)
		}
	}
	g := tab.nodes[n].gut
	cx, cy := (g[0]+g[2])/2, (g[1]+g[3])/2
	agent, git := h.paneSlot(kindAgent), h.paneSlot(kindGit)
	y1 := h.rectOf(agent)[3]
	h.move(cx, cy)
	h.frame()
	h.frame()
	if h.cursor() != ui.CursorResizeNS {
		t.Errorf("cursor over the horizontal divider = %v", h.cursor())
	}
	h.in.SetButton(ui.ButtonLeft, true, 1)
	h.frame()
	for _, dy := range []float32{30, 90, 150, 60, -80} {
		h.move(cx, cy+dy)
		h.frame()
		if got := h.rectOf(agent)[3] - y1; math.Abs(float64(got-dy)) > 1 {
			t.Errorf("dragged by %v: divider moved %v", dy, got)
		}
		if got := h.rectOf(git)[1] - h.rectOf(agent)[3]; got != 15 {
			t.Errorf("gutter %v while dragging", got)
		}
	}
	h.move(cx, 5000)
	h.frame()
	if hgt := h.rectOf(git)[3] - h.rectOf(git)[1]; hgt < h.x.gutters().minH-1 {
		t.Errorf("git pane %v px high, minimum %v", hgt, h.x.gutters().minH)
	}
	h.in.SetButton(ui.ButtonLeft, false, 0)
	h.frame()
	h.settle()
	h.checkLayout(WindowWidthPt, WindowHeightPt, 2)
}

func TestSplitMaximiseClose(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	agent := h.paneSlot(kindAgent)
	old := h.rectOf(agent)

	// Split side by side: the focused pane's rectangle is shared by two panes.
	h.click(h.btn(agent, btnSplitX))
	if h.cur().numPanes() != 4 {
		t.Fatalf("%d panes after a split", h.cur().numPanes())
	}
	if !h.c.Animating() {
		t.Error("a split does not animate")
	}
	h.settle()
	shell := h.paneSlot(kindShell)
	a, s := h.rectOf(agent), h.rectOf(shell)
	if a[0] != old[0] || s[2] != old[2] || a[1] != old[1] || s[3] != old[3] || s[0]-a[2] != 14 {
		t.Errorf("split: agent %v, new pane %v, was %v", a, s, old)
	}
	if h.cur().focus != h.cur().panes[shell].uid {
		t.Error("the new pane does not take focus")
	}
	if math.Abs(float64((a[2]-a[0])-(s[2]-s[0]))) > 15 {
		t.Errorf("halves differ: %v vs %v", a[2]-a[0], s[2]-s[0])
	}
	h.checkLayout(WindowWidthPt, WindowHeightPt, 2)

	// Split the new pane stacked.
	h.click(h.btn(shell, btnSplitY))
	h.settle()
	if h.cur().numPanes() != 5 {
		t.Fatalf("%d panes", h.cur().numPanes())
	}
	h.checkLayout(WindowWidthPt, WindowHeightPt, 2)

	// Maximise the focused pane: it grows to the work area, animated.
	uid := h.cur().focus
	fslot := int(h.cur().slotOf(uid))
	before := h.rectOf(fslot)
	h.click(h.btn(fslot, btnMax))
	if h.cur().max != uid {
		t.Fatal("maximise did not take")
	}
	sawMid := false
	work := h.x.work()
	for i := 0; i < 300 && h.c.Animating(); i++ {
		h.frame()
		if r := h.rectOf(fslot); r != before && r != work {
			sawMid = true
		}
	}
	if !sawMid {
		t.Error("maximise did not animate through intermediate sizes")
	}
	if h.rectOf(fslot) != work {
		t.Errorf("maximised pane at %v, work area %v", h.rectOf(fslot), work)
	}
	// Other panes are no longer drawn: no quad of the screen except the maximised pane's lies in the work area interior outside it.
	// Restore.
	h.click(h.btn(fslot, btnMax))
	h.settle()
	if h.cur().max != 0 || h.rectOf(fslot) != before {
		t.Errorf("restore: max %d, pane at %v, was %v", h.cur().max, h.rectOf(fslot), before)
	}

	// Close the focused pane: its sibling takes the space and the focus.
	h.click(h.btn(fslot, btnClose))
	h.settle()
	if h.cur().numPanes() != 4 {
		t.Fatalf("%d panes after a close", h.cur().numPanes())
	}
	if h.cur().focus == uid || h.cur().slotOf(h.cur().focus) < 0 {
		t.Errorf("focus %d after closing %d", h.cur().focus, uid)
	}
	h.checkLayout(WindowWidthPt, WindowHeightPt, 2)

	// Back to the reference arrangement by closing the two extra panes.
	for h.cur().numPanes() > 3 {
		fs := int(h.cur().slotOf(h.cur().focus))
		h.click(h.btn(fs, btnClose))
		h.settle()
	}
	h.checkLayout(WindowWidthPt, WindowHeightPt, 2)
}

// The header buttons exist on the focused pane only: a click where an
// unfocused pane's button would be focuses the pane and does nothing else.
func TestButtonsOnlyOnFocusedPane(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	git := h.paneSlot(kindGit)
	h.click(h.btn(git, btnClose))
	if h.cur().numPanes() != 3 {
		t.Fatalf("an unfocused pane's close button closed it")
	}
	if h.cur().focus != h.cur().panes[git].uid {
		t.Error("the click did not focus the pane")
	}
}

func TestCursorHints(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	agent := h.paneSlot(kindAgent)
	W := float32(h.w)
	k := h.x.k
	check := func(name string, x, y float32, want ui.Cursor) {
		t.Helper()
		h.move(x, y)
		h.settle()
		if got := h.cursor(); got != want {
			t.Errorf("%s: cursor %v, want %v", name, got, want)
		}
	}
	check("window background", 4, 4, ui.CursorArrow)
	bx, by := h.blank(agent)
	check("pane body", bx, by, ui.CursorArrow)
	tx, ty := centre(h.tabRect(1))
	check("tab", tx, ty, ui.CursorPointer)
	check("+", W-46.5*k, 45*k, ui.CursorPointer)
	check("command", W-115.5*k, 45*k, ui.CursorPointer)
	mx, my := h.btn(agent, btnMax)
	check("maximise button", mx, my, ui.CursorPointer)
	g, _ := h.rootDivider()
	check("vertical divider", (g[0]+g[2])/2, (g[1]+g[3])/2, ui.CursorResizeEW)
	check("vertical divider, grab margin", g[0]-3, (g[1]+g[3])/2, ui.CursorResizeEW)
	var hg [4]float32
	for i := range h.cur().nodes {
		if n := &h.cur().nodes[i]; n.used && !n.leaf && n.axis == ui.AxisY {
			hg = n.gut
		}
	}
	check("horizontal divider", (hg[0]+hg[2])/2, (hg[1]+hg[3])/2, ui.CursorResizeNS)
}

// Degenerate windows (minimised, a sliver, a few pixels) build without panics.
func TestDegenerateSizes(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	for _, sz := range [][2]int{{0, 0}, {1, 1}, {40, 30}, {100, 60}, {300, 200}, {5000, 40}, {40, 5000}, {2114, 1278}} {
		h.w, h.h = sz[0], sz[1]
		h.frame()
		h.move(float32(sz[0])/2, float32(sz[1])/2)
		h.click(float32(sz[0])/2, float32(sz[1])/2)
		for _, q := range h.list.Quads {
			for _, v := range q.Rect {
				if v != v || v > 1e6 || v < -1e6 {
					t.Fatalf("%v: quad with rect %v", sz, q.Rect)
				}
			}
		}
	}
}

// A change of scale (the window moves to another display) recreates the fonts
// and the cell grid follows: 7.5 pt per cell at any scale.
func TestScaleChange(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	for _, s := range []float32{1, 3, 2, 1.5} {
		h.scale = s
		h.w, h.h = int(WindowWidthPt*s), int(WindowHeightPt*s)
		h.settle()
		if got := h.x.CellAdvancePt(); math.Abs(got-7.5) > 0.05 {
			t.Errorf("scale %v: cell advance %.3f pt, want 7.5", s, got)
		}
		if got := h.x.RowPitchPt(); math.Abs(got-16) > 0.001 {
			t.Errorf("scale %v: row pitch %.3f pt, want 16", s, got)
		}
		h.checkLayout(WindowWidthPt, WindowHeightPt, s)
	}
}

// Nothing animates or changes once the state has settled: an idle window
// asks for no frames.
func TestIdleWhenSettled(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	h.move(5, 5)
	h.settle()
	h.frame()
	first := append([]gpu.Quad(nil), h.list.Quads...)
	for i := 0; i < 90; i++ {
		h.frame()
		if h.c.Animating() {
			t.Fatalf("frame %d: still animating at rest", i)
		}
	}
	if len(first) != len(h.list.Quads) {
		t.Fatalf("draw list changed at rest: %d → %d quads", len(first), len(h.list.Quads))
	}
	for i := range first {
		if first[i] != h.list.Quads[i] {
			t.Fatalf("quad %d changed at rest", i)
		}
	}
	// And again after a click-driven change.
	snake := h.paneSlot(kindSnake)
	h.click(h.blank(snake))
	h.settle()
	for i := 0; i < 30; i++ {
		h.frame()
		if h.c.Animating() {
			t.Fatal("animating at rest after a focus change")
		}
	}
}

// dragAt decides a press the way the platform does with the map the window
// loop hands it: the last rectangle that contains the point.
func dragAt(regs []ui.DragRegion, x, y float32) bool {
	for i := len(regs) - 1; i >= 0; i-- {
		r := regs[i].Rect
		if x >= r[0] && x < r[2] && y >= r[1] && y < r[3] {
			return !regs[i].Block
		}
	}
	return false
}

// The titlebar band moves the window except where a tab or a button is.
func TestTitlebarDragMap(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		h := newHarness(t, WindowWidthPt, WindowHeightPt, scale)
		regs := h.c.DragRegions(nil)
		if len(regs) < 5 {
			t.Fatalf("scale %v: %d drag regions %v", scale, len(regs), regs)
		}
		bar := scale * TitlebarPt
		W := float32(h.w)
		k := h.x.k
		for _, c := range []struct {
			name string
			x, y float32
			want bool
		}{
			{"band, left of the host block", 100 * scale, 10 * scale, true},
			{"band, right of the capsule", W - 150*scale, 23 * scale, true},
			{"band, above the capsule", W / 2, 2 * scale, true},
			{"band, last row, under the tabs", 330 * scale, bar - 1, true},
			{"host block text", 130 * scale, 22 * scale, true},
			{"band between capsule and +, last row", W - 180*scale, bar - 1, true},
			{"tab 1", 330 * scale, 23 * scale, false},
			{"tab 2", 330*scale + (h.tabRect(1)[0] - h.tabRect(0)[0]), 23 * scale, false},
			{"+", W - 46.5*k, 45 * k, false},
			{"command", W - 115.5*k, 45 * k, false},
			{"just below the band", 330 * scale, bar + 1, false},
			{"pane body", 400 * scale, 300 * scale, false},
		} {
			if got := dragAt(regs, c.x, c.y); got != c.want {
				t.Errorf("scale %v, %s (%v,%v): drag %v, want %v", scale, c.name, c.x, c.y, got, c.want)
			}
		}
	}
}

// A divider's grab area is at least 8 pt across and centred on the gutter,
// and spans the whole split, at every scale.
func TestDividerGrabArea(t *testing.T) {
	for _, scale := range []float32{1, 2, 3} {
		h := newHarness(t, WindowWidthPt, WindowHeightPt, scale)
		regs := h.c.CursorRegions(nil)
		found := 0
		for i := range h.cur().nodes {
			n := &h.cur().nodes[i]
			if !n.used || n.leaf {
				continue
			}
			want, ax := ui.CursorResizeEW, 0
			if n.axis == ui.AxisY {
				want, ax = ui.CursorResizeNS, 1
			}
			cx, cy := centre(n.gut)
			var got [4]float32
			ok := false
			for _, r := range regs {
				if r.Cursor == want && cx >= r.Rect[0] && cx < r.Rect[2] && cy >= r.Rect[1] && cy < r.Rect[3] {
					got, ok = r.Rect, true
				}
			}
			if !ok {
				t.Fatalf("scale %v: no %v region over the gutter %v", scale, want, n.gut)
			}
			found++
			if w := got[ax+2] - got[ax]; w < 8*scale {
				t.Errorf("scale %v: grab area %v pt across, want at least 8", scale, w/scale)
			}
			if mid, gm := (got[ax]+got[ax+2])/2, (n.gut[ax]+n.gut[ax+2])/2; mid < gm-1 || mid > gm+1 {
				t.Errorf("scale %v: grab area %v is not centred on the gutter %v", scale, got, n.gut)
			}
			other := 1 - ax
			if got[other] > n.rect[other]+1 || got[other+2] < n.rect[other+2]-1 {
				t.Errorf("scale %v: grab area %v does not span the split %v", scale, got, n.rect)
			}
		}
		if found != 2 {
			t.Errorf("scale %v: %d dividers, want 2", scale, found)
		}
	}
}
