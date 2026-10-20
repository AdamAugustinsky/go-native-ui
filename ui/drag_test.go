package ui

import (
	"testing"
)

// dropRig is a 400×300 window with an outer target (the "list", 20,20..220,220
// and clipping), an inner target over it (the "card", 60,60..160,120: floating
// boxes are placed from their parent's corner) that takes files only, a box that
// sticks out of the list's clip (200,200..300,300 of which 200..220 shows), and
// a text-only target to the right (260,20..360,120).
type dropRig struct {
	*harness
	list, card, side, none     ID
	listSig, cardSig, sideSig  DropSignal
	noneSig                    DropSignal
	modal, popup, disableCard  bool
	cardOps                    DropOp
	listKinds, cardKinds       DropKinds
	dropped                    []string
	cardClickable, hideSideBox bool
}

func newDropRig(t *testing.T) *dropRig {
	r := &dropRig{harness: newHarness(t, 400, 300), listKinds: DropAny, cardKinds: DropFiles}
	r.in.Inside = false
	r.settle()
	return r
}

func (r *dropRig) settle() {
	for i := 0; i < 3; i++ {
		r.frame(r.build)
	}
}

func (r *dropRig) build(c *Context) {
	place := func(x, y, w, h float32, flags Flags, label string) ID {
		c.NextPos(x, y)
		c.NextSize(Px(w), Px(h))
		return c.Box(flags|FloatingX|FloatingY, label)
	}
	r.list = place(20, 20, 200, 200, Clip, "list")
	c.AcceptDrop(r.list, DropSpec{Kinds: r.listKinds, Ops: DropCopy | DropLink})
	c.Push(r.list)
	f := Flags(0)
	if r.cardClickable {
		f |= Clickable
	}
	if r.disableCard {
		f |= Disabled
	}
	r.card = place(40, 40, 100, 60, f, "card")
	c.AcceptDrop(r.card, DropSpec{Kinds: r.cardKinds, Ops: r.cardOps})
	// A box inside the clipping list that sticks out of it.
	r.none = place(180, 180, 100, 100, 0, "sticks out")
	c.AcceptDrop(r.none, DropSpec{Kinds: DropAny})
	c.Pop()
	r.side = place(260, 20, 100, 100, 0, "side")
	if !r.hideSideBox {
		c.AcceptDrop(r.side, DropSpec{Kinds: DropText})
	}
	if r.popup {
		r.popupOver()
	}
	r.listSig, r.cardSig, r.sideSig, r.noneSig = c.DropSignal(r.list), c.DropSignal(r.card), c.DropSignal(r.side), c.DropSignal(r.none)
	if r.modal {
		c.BeginOverlay(7, OverlayModal, [4]float32{0, 0, 10, 10}, PlaceBelow)
		c.NextSize(Px(30), Px(30))
		c.Box(0, "dialog")
		c.EndOverlay()
	}
}

func (r *dropRig) popupOver() {
	c := r.c
	c.BeginOverlay(3, OverlayPopup, [4]float32{50, 40, 90, 40}, PlaceBelow)
	c.NextSize(Px(60), Px(40))
	c.NextBG(0xFF)
	c.Box(DrawBackground, "popup")
	c.EndOverlay()
}

func (r *dropRig) over(x, y float32, offered DropKinds, allowed DropOp) {
	r.in.Drop.SetDragOver(x, y, offered, allowed)
	r.frame(r.build)
}

func TestDropTargetsAreFoundTopmostFirst(t *testing.T) {
	r := newDropRig(t)
	files := DropFiles

	// Over the card: it is the target, and the list under it is not.
	r.over(80, 80, files, DropAllOps)
	if !r.cardSig.Over || r.listSig.Over || r.sideSig.Over {
		t.Fatalf("card/list/side: %+v %+v %+v", r.cardSig, r.listSig, r.sideSig)
	}
	if r.cardSig.Op != DropCopy || r.cardSig.Offered != files || r.cardSig.Pos != [2]float32{20, 20} {
		t.Errorf("card signal %+v (the card names no operation, so copy; the box is at 60,60)", r.cardSig)
	}

	// The card takes files only: for text the list is the target.
	r.over(80, 80, DropText, DropAllOps)
	if r.cardSig.Over || !r.listSig.Over || r.listSig.Pos != [2]float32{60, 60} {
		t.Errorf("text over the card: %+v %+v", r.cardSig, r.listSig)
	}

	// The operation is the first the source allows among the target's.
	r.over(80, 80, DropText, DropLink|DropMove)
	if r.listSig.Op != DropLink {
		t.Errorf("link-only source: %+v", r.listSig)
	}
	r.over(80, 80, DropText, DropMove)
	if r.listSig.Op != DropNone || !r.listSig.Over {
		t.Errorf("a source that only moves, a target that copies and links: %+v", r.listSig)
	}

	// Only the text target takes text on the right; nothing outside boxes.
	r.over(300, 60, DropText, DropAllOps)
	if !r.sideSig.Over {
		t.Errorf("side: %+v", r.sideSig)
	}
	r.over(300, 60, DropFiles, DropAllOps)
	if r.sideSig.Over {
		t.Errorf("side took files: %+v", r.sideSig)
	}
	r.over(380, 280, DropAny, DropAllOps)
	if r.listSig.Over || r.cardSig.Over || r.sideSig.Over || r.noneSig.Over {
		t.Errorf("empty window: %+v %+v %+v", r.listSig, r.cardSig, r.sideSig)
	}

	// A box clipped by its parent takes drags only where it shows.
	r.over(250, 250, DropAny, DropAllOps)
	if r.noneSig.Over {
		t.Error("the part of a box that its clipping parent hides took a drag")
	}
	r.over(210, 210, DropAny, DropAllOps)
	if !r.noneSig.Over {
		t.Errorf("the visible part of the box did not: %+v", r.noneSig)
	}

	// No drag over the window: nothing is Over.
	r.in.Drop.ClearDragOver()
	r.frame(r.build)
	if r.cardSig.Over || r.listSig.Over || r.c.DragActive() {
		t.Errorf("after the drag left: %+v %+v", r.cardSig, r.listSig)
	}
}

func TestDropTargetStates(t *testing.T) {
	r := newDropRig(t)
	r.over(80, 80, DropFiles, DropAllOps)
	if !r.cardSig.Over {
		t.Fatal("setup")
	}
	r.disableCard = true
	r.frame(r.build) // the target is found in the layout of the frame before
	r.over(80, 80, DropFiles, DropAllOps)
	if r.cardSig.Over || !r.listSig.Over {
		t.Errorf("a disabled target took the drag: %+v %+v", r.cardSig, r.listSig)
	}
	r.disableCard = false

	// A popup over the target: the pointer is the popup's.
	r.popup = true
	r.settle()
	r.over(80, 70, DropFiles, DropAllOps)
	if r.cardSig.Over || r.listSig.Over {
		t.Errorf("a target under a popup took the drag: %+v %+v", r.cardSig, r.listSig)
	}
	if op, _ := r.c.DropAt(80, 70, DropFiles, DropAllOps); op != DropNone {
		t.Errorf("DropAt through a popup answered %v", op)
	}
	r.popup = false
	r.settle()

	// A modal takes everything.
	r.modal = true
	r.settle()
	r.over(80, 80, DropFiles, DropAllOps)
	if r.cardSig.Over || r.listSig.Over || r.sideSig.Over {
		t.Errorf("targets under a modal took the drag: %+v %+v %+v", r.cardSig, r.listSig, r.sideSig)
	}
	r.modal = false
	r.settle()

	// A target that stops being declared stops taking drags.
	r.hideSideBox = true
	r.settle()
	r.over(300, 60, DropText, DropAllOps)
	if r.sideSig.Over {
		t.Error("an undeclared target took the drag")
	}
}

func TestDropAtBetweenFrames(t *testing.T) {
	r := newDropRig(t)
	// What the platform handler asks, with no frame in between.
	if op, accept := r.c.DropAt(80, 80, DropFiles, DropAllOps); op != DropCopy || accept != DropFiles {
		t.Errorf("card: %v %v", op, accept)
	}
	if op, accept := r.c.DropAt(80, 80, DropImage, DropAllOps); op != DropCopy || accept != DropAny {
		t.Errorf("image over the card falls to the list: %v %v", op, accept)
	}
	if op, accept := r.c.DropAt(80, 80, DropFiles, DropMove); op != DropNone || accept != DropFiles {
		// the card copies only: nothing to answer a move-only source
		t.Errorf("move-only source: %v %v", op, accept)
	}
	if op, accept := r.c.DropAt(390, 290, DropAny, DropAllOps); op != DropNone || accept != 0 {
		t.Errorf("outside: %v %v", op, accept)
	}
	if k := r.c.DropKinds(); k != DropAny {
		t.Errorf("kinds %v", k)
	}
	// A frame that declares nothing leaves no stale targets.
	r.frame(nil)
	if k := r.c.DropKinds(); k != 0 {
		t.Errorf("kinds after a frame with no targets: %v", k)
	}
	if op, _ := r.c.DropAt(80, 80, DropFiles, DropAllOps); op != DropNone {
		t.Errorf("stale target answered %v", op)
	}
}

func TestDropIsDeliveredToTheTargetUnderThePointer(t *testing.T) {
	r := newDropRig(t)
	paths := "/a/b.txt\x00/c d/\u65e5\u672c.txt\x00"
	r.in.Drop.SetDrop(DropFiles, []byte(paths), 90, 70, 2, true)
	r.frame(r.build)
	if !r.cardSig.Dropped || r.listSig.Dropped || r.cardSig.Over {
		t.Fatalf("signals %+v %+v", r.cardSig, r.listSig)
	}
	if r.cardSig.Pos != [2]float32{30, 10} {
		t.Errorf("drop position %v", r.cardSig.Pos)
	}
	d := r.c.Drop()
	got := d.Paths(nil)
	if d.Kind != DropFiles || d.Items != 2 || len(got) != 2 || got[0] != "/a/b.txt" || got[1] != "/c d/\u65e5\u672c.txt" || d.Pos != [2]float32{90, 70} {
		t.Errorf("drop %+v paths %q", d, got)
	}
	if d.Text() != "" {
		t.Error("text of a file drop")
	}

	// One frame only.
	r.frame(r.build)
	if r.cardSig.Dropped || r.c.Drop().Kind != 0 {
		t.Errorf("the drop stayed: %+v %+v", r.cardSig, r.c.Drop())
	}

	// Text, to the text target; a drop where nothing takes it reaches nobody.
	r.in.Drop.SetDrop(DropText, []byte("h\u00e9llo"), 300, 50, 1, true)
	r.frame(r.build)
	if !r.sideSig.Dropped || r.c.Drop().Text() != "h\u00e9llo" {
		t.Errorf("text drop: %+v %q", r.sideSig, r.c.Drop().Text())
	}
	r.in.Drop.SetDrop(DropImage, []byte("png"), 300, 50, 1, true)
	r.frame(r.build)
	if r.sideSig.Dropped || r.c.Drop().Kind != 0 {
		t.Errorf("an image dropped on a text target: %+v", r.sideSig)
	}

	// A failed drop is still delivered, without data.
	r.in.Drop.SetDrop(DropFiles, nil, 80, 80, 0, false)
	r.frame(r.build)
	if d := r.c.Drop(); !r.cardSig.Dropped || !d.Failed || len(d.Data) != 0 {
		t.Errorf("failed drop: %+v %+v", r.cardSig, d)
	}

	// A drop ends the drag over the window.
	r.in.Drop.SetDragOver(80, 80, DropFiles, DropAllOps)
	r.in.Drop.SetDrop(DropFiles, []byte("/x\x00"), 80, 80, 1, true)
	if r.in.Drop.Drag.Active {
		t.Error("a drop left the drag active")
	}
}

func TestDropInputStorage(t *testing.T) {
	var in Input
	big := make([]byte, 1<<20)
	in.Drop.SetDrop(DropImage, big, 1, 1, 1, true)
	in.ClearFrame()
	if cap(in.Drop.Data) < len(big) {
		t.Fatal("the storage was not kept")
	}
	if n := testing.AllocsPerRun(50, func() {
		in.Drop.SetDrop(DropImage, big, 1, 1, 1, true)
		in.ClearFrame()
	}); n != 0 {
		t.Errorf("a repeated drop allocates %v times", n)
	}
	in.Drop.SetDrop(DropText, make([]byte, MaxKeptPaste+1), 1, 1, 1, true)
	in.ClearFrame()
	if cap(in.Drop.Data) > MaxKeptPaste {
		t.Error("more than a supported drop is kept")
	}
	in.Drop.ReleaseDrop()
	if in.Drop.Data != nil {
		t.Error("ReleaseDrop")
	}
}

func TestDropSteadyStateAllocatesNothing(t *testing.T) {
	r := newDropRig(t)
	r.in.Drop.SetDragOver(80, 80, DropFiles, DropAllOps)
	r.frame(r.build)
	n := testing.AllocsPerRun(100, func() {
		r.frame(r.build)
		r.c.DropAt(80, 80, DropFiles, DropAllOps)
	})
	if n != 0 {
		t.Errorf("a frame with a drag over declared targets allocates %v times", n)
	}
	r.in.Drop.ClearDragOver()
	r.frame(r.build)
	if n := testing.AllocsPerRun(100, func() { r.frame(r.build) }); n != 0 {
		t.Errorf("an idle frame with targets allocates %v times", n)
	}
}

// ---- drag out ----

func TestDragOutStartsPastTheThreshold(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.c.SetDragThreshold(4)
	var id ID
	var sig Signal
	var started, exceeded bool
	build := func(c *Context) {
		c.NextPos(50, 50)
		c.NextSize(Px(80), Px(40))
		id = c.Box(Clickable|FloatingX|FloatingY, "src")
		sig = c.Signal(id)
		exceeded = c.DragExceeded(sig)
		if exceeded {
			started = c.StartDrag(id, DragSpec{Files: []string{"/tmp/a"}, Ops: DropCopy | DropMove})
		}
	}
	h.frame(build)
	h.frame(build)
	h.move(60, 60)
	h.frame(build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(build)
	if exceeded || !sig.Dragging {
		t.Fatalf("pressed: dragging %v exceeded %v", sig.Dragging, exceeded)
	}
	h.move(62, 61) // 2 px
	h.frame(build)
	if exceeded {
		t.Error("2 px is not a drag")
	}
	h.move(63, 63) // 3,3 px = 4.24
	h.frame(build)
	if !exceeded || !started {
		t.Fatalf("4.2 px: exceeded %v started %v", exceeded, started)
	}
	if h.c.StartDrag(id, DragSpec{Text: "again"}) {
		t.Error("a second request while one is pending")
	}
	req, ok := h.c.TakeDragStart()
	if !ok || len(req.Spec.Files) != 1 || req.Spec.Ops != DropCopy|DropMove || req.Box != [4]float32{50, 50, 130, 90} {
		t.Fatalf("request %+v %v", req, ok)
	}
	if _, ok := h.c.TakeDragStart(); ok {
		t.Error("taken twice")
	}
	if !h.c.DragRunning() || h.c.StartDrag(id, DragSpec{Text: "x"}) {
		t.Error("a drag can start while one runs")
	}
	exceeded = false
	h.frame(build)
	if exceeded {
		t.Error("exceeded while the drag runs")
	}

	// The system took the mouse: the app cancels the press, nobody is clicked.
	h.in.Cancel(ButtonLeft)
	h.frame(build)
	if sig.Clicked || sig.Released || sig.Dragging {
		t.Errorf("after the cancel: %+v", sig)
	}

	// The end of the drag: the operation, and a new one may start.
	h.in.Drop.EndDrag(DropMove)
	h.frame(build)
	if op, ended := h.c.DragEnd(); !ended || op != DropMove || h.c.DragRunning() {
		t.Errorf("end: %v %v running %v", op, ended, h.c.DragRunning())
	}
	h.frame(build)
	if _, ended := h.c.DragEnd(); ended {
		t.Error("the end stayed for another frame")
	}

	// A drag the system refused can be asked for again.
	if !h.c.StartDrag(id, DragSpec{Text: "t"}) {
		t.Fatal("StartDrag")
	}
	h.c.TakeDragStart()
	h.c.DragStarted(false)
	if h.c.DragRunning() || !h.c.StartDrag(id, DragSpec{Text: "t"}) {
		t.Error("a refused drag still runs")
	}
	if h.c.StartDrag(0, DragSpec{Text: "t"}) {
		t.Error("a drag from no box")
	}
}

func TestDragThresholdScales(t *testing.T) {
	c := New(newFake())
	if got := c.DragThreshold(); got != 4 {
		t.Errorf("default threshold %v px", got)
	}
	c.SetDragThreshold(8)
	c.scale = 2
	if got := c.DragThreshold(); got != 16 {
		t.Errorf("8 points at scale 2 is %v px", got)
	}
}

func TestStartDragNeedsSomethingToDrag(t *testing.T) {
	h := newHarness(t, 100, 100)
	var id ID
	h.frame(func(c *Context) { id = c.Box(Clickable, "x") })
	if h.c.StartDrag(id, DragSpec{}) {
		t.Error("an empty drag started")
	}
	if !h.c.StartDrag(id, DragSpec{Promises: []FilePromise{{Name: "a"}}}) {
		t.Error("a promise is something to drag")
	}
}

// A source in a custom titlebar must keep its presses: boxes that take input are
// Block regions of the window-drag map, so the press is the app's.
func TestDragSourceInATitlebarKeepsItsPress(t *testing.T) {
	h := newHarness(t, 400, 300)
	build := func(c *Context) {
		c.NextPos(0, 0)
		c.NextSize(Px(400), Px(40))
		bar := c.Box(WindowDrag|FloatingX|FloatingY, "bar")
		c.Push(bar)
		c.NextPos(100, 5)
		c.NextSize(Px(60), Px(30))
		src := c.Box(Clickable|FloatingX|FloatingY, "src")
		c.Signal(src)
		c.Pop()
	}
	h.frame(build)
	h.frame(build)
	regs := h.c.DragRegions(nil)
	at := func(x, y float32) bool { // the platform's rule: the last region containing the point decides
		drag := false
		for _, r := range regs {
			if x >= r.Rect[0] && x < r.Rect[2] && y >= r.Rect[1] && y < r.Rect[3] {
				drag = !r.Block
			}
		}
		return drag
	}
	if !at(10, 20) || at(120, 20) {
		t.Errorf("regions %+v: the bar must move the window and the source must not", regs)
	}
}
