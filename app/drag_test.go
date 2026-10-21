package app

import (
	"bytes"
	"errors"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/platform"
	"gonativeui/ui"
)

// fakeDrag records what reaches the window's drag calls.
type fakeDrag struct {
	kinds []platform.DragKinds
	specs []platform.DragSpec
	err   error
}

func (f *fakeDrag) SetDropKinds(k platform.DragKinds) { f.kinds = append(f.kinds, k) }

func (f *fakeDrag) StartDrag(s *platform.DragSpec) (platform.DragTicket, error) {
	f.specs = append(f.specs, *s)
	if f.err != nil {
		return 0, f.err
	}
	return platform.DragTicket(len(f.specs)), nil
}

// dragApp is an App with a UI and no window, and a builder with a text target
// at (20,20)-(120,70) that also starts a drag when asked.
type dragApp struct {
	*App
	list  gpu.DrawList
	box   ui.ID
	sig   ui.DropSignal
	drop  ui.DropData
	got   bool
	start *ui.DragSpec
	end   ui.DropOp
	ended bool
}

func newDragApp() *dragApp {
	d := &dragApp{App: &App{appObject: &appObject{}, UI: ui.New(fakeui.New()), tr: newTranslator(), p: newPacer()}}
	d.tr.in.Inside = false
	return d
}

func (d *dragApp) frame() {
	a := d.App
	a.UI.Begin(&a.tr.in, 400, 300, 1, 1.0/60)
	a.UI.NextPos(20, 20)
	a.UI.NextSize(ui.Px(100), ui.Px(50))
	d.box = a.UI.Box(ui.Clickable|ui.FloatingX|ui.FloatingY, "target")
	a.UI.AcceptDrop(d.box, ui.DropSpec{Kinds: ui.DropText | ui.DropFiles})
	d.sig = a.UI.DropSignal(d.box)
	d.got = d.sig.Dropped
	d.drop = a.UI.Drop()
	if d.drop.Kind != 0 {
		d.drop.Data = append([]byte(nil), d.drop.Data...) // the Input reuses its storage
	}
	d.end, d.ended = a.UI.DragEnd()
	if d.start != nil {
		a.UI.StartDrag(d.box, *d.start)
		d.start = nil
	}
	a.UI.End(&d.list)
	a.tr.endFrame()
}

func TestDragEventsReachTheNextFrame(t *testing.T) {
	d := newDragApp()
	d.frame()
	d.frame()

	// A drag enters, moves and leaves: the target is Over while it is on it.
	d.onDrag(&platform.Event{Kind: platform.EvDragEnter, X: 50, Y: 40, Code: uint32(platform.DragText), SelStart: int32(platform.DragOpAll), Time: 5}, nil)
	if !d.p.needDraw {
		t.Error("a drag over the window asks for no frame")
	}
	d.frame()
	if !d.sig.Over || d.sig.Op != ui.DropCopy || d.sig.Offered != ui.DropText || d.sig.Pos != [2]float32{30, 20} {
		t.Fatalf("over: %+v", d.sig)
	}
	d.onDrag(&platform.Event{Kind: platform.EvDragMove, X: 300, Y: 40, Code: uint32(platform.DragText), SelStart: int32(platform.DragOpAll)}, nil)
	d.frame()
	if d.sig.Over {
		t.Errorf("over after moving off: %+v", d.sig)
	}
	d.onDrag(&platform.Event{Kind: platform.EvDragMove, X: 50, Y: 40, Code: uint32(platform.DragText), SelStart: int32(platform.DragOpAll)}, nil)
	d.onDrag(&platform.Event{Kind: platform.EvDragLeave}, nil)
	d.frame()
	if d.sig.Over || d.UI.DragActive() {
		t.Errorf("over after the drag left: %+v", d.sig)
	}

	// The drop: the data arrives with the signal, for one frame.
	d.onDrag(&platform.Event{Kind: platform.EvDragEnter, X: 50, Y: 40, Code: uint32(platform.DragFiles)}, nil)
	d.onDrag(&platform.Event{Kind: platform.EvDrop, X: 50, Y: 40, Code: uint32(platform.DragFiles), SelStart: 2}, []byte("/a\x00/b\x00"))
	d.frame()
	if !d.got || d.sig.Over || d.drop.Kind != ui.DropFiles || d.drop.Items != 2 || len(d.drop.Paths(nil)) != 2 {
		t.Fatalf("drop: %+v %+v", d.sig, d.drop)
	}
	d.frame()
	if d.got || d.UI.DragActive() {
		t.Errorf("drop stayed: %+v", d.sig)
	}
	d.onDrag(&platform.Event{Kind: platform.EvDrop, X: 50, Y: 40, Code: uint32(platform.DragText), Flags: platform.FlagFailed}, nil)
	d.frame()
	if !d.got || !d.drop.Failed {
		t.Errorf("failed drop: %+v %+v", d.sig, d.drop)
	}

	// The end of a drag this app started.
	d.onDrag(&platform.Event{Kind: platform.EvDragEnd, Code: uint32(platform.DragOpMove)}, nil)
	d.frame()
	if !d.ended || d.end != ui.DropMove {
		t.Errorf("end: %v %v", d.end, d.ended)
	}
}

func TestDragAnswerComesFromTheLastFrame(t *testing.T) {
	d := newDragApp()
	d.frame()
	d.frame()
	if a := d.dragAnswer(50, 40, platform.DragText, platform.DragOpAll); a.Op != platform.DragOpCopy || a.Accept != platform.DragText|platform.DragFiles {
		t.Errorf("on the target: %+v", a)
	}
	if a := d.dragAnswer(50, 40, platform.DragImage, platform.DragOpAll); a.Op != platform.DragOpNone || a.Accept != 0 {
		t.Errorf("a kind it does not take: %+v", a)
	}
	if a := d.dragAnswer(300, 40, platform.DragText, platform.DragOpAll); a.Op != platform.DragOpNone {
		t.Errorf("off the target: %+v", a)
	}
	if n := testing.AllocsPerRun(100, func() { d.dragAnswer(50, 40, platform.DragText, platform.DragOpAll) }); n != 0 {
		t.Errorf("the answer allocates %v times", n)
	}
}

func TestFlushDragRegistersKindsAndStartsDrags(t *testing.T) {
	d := newDragApp()
	win := &fakeDrag{}
	d.dragT = win
	d.frame()
	d.flushDrag()
	if len(win.kinds) != 1 || win.kinds[0] != platform.DragText|platform.DragFiles || len(win.specs) != 0 {
		t.Fatalf("after a frame: kinds %v specs %d", win.kinds, len(win.specs))
	}

	// A press, then the build asks for a drag.
	d.tr.in.Inside = true
	d.tr.in.MouseX, d.tr.in.MouseY = 40, 30
	d.frame()
	d.tr.in.SetButton(ui.ButtonLeft, true, 1)
	d.frame()
	promise := ui.FilePromise{Name: "clip.mp4", Type: "public.mpeg-4"}
	d.start = &ui.DragSpec{Files: []string{"/tmp/a", "/tmp/b"}, Text: "t", Image: []byte("png"), Promises: []ui.FilePromise{promise}, Ops: ui.DropCopy | ui.DropMove, Preview: []byte("pic")}
	d.frame()
	d.flushDrag()
	if len(win.specs) != 1 {
		t.Fatalf("%d drags started", len(win.specs))
	}
	s := win.specs[0]
	if len(s.Items) != 5 || s.Items[0].Path != "/tmp/a" || s.Items[1].Path != "/tmp/b" || s.Items[2].Text != "t" || string(s.Items[3].PNG) != "png" ||
		s.Items[4].Promise == nil || s.Items[4].Promise.Name != "clip.mp4" || s.Ops != platform.DragOpCopy|platform.DragOpMove {
		t.Errorf("spec %+v", s)
	}
	if s.Preview == nil || string(s.Preview.PNG) != "pic" || s.Preview.Rect != [4]float32{20, 20, 120, 70} {
		t.Errorf("preview %+v", s.Preview)
	}
	if !d.UI.DragRunning() {
		t.Error("the drag is not running")
	}
	// The press is cancelled: the system has the mouse, the release never comes.
	if !d.tr.in.Cancelled[ui.ButtonLeft] || d.tr.in.Down[ui.ButtonLeft] {
		t.Errorf("press not cancelled: %+v", d.tr.in)
	}
	d.frame()
	d.flushDrag()
	if len(win.specs) != 1 {
		t.Error("a drag started again")
	}

	// A drag the system refuses leaves the press alone and can be asked for again.
	d.onDrag(&platform.Event{Kind: platform.EvDragEnd}, nil)
	d.frame()
	win.err = errors.New("no")
	d.tr.in.SetButton(ui.ButtonLeft, true, 1)
	d.start = &ui.DragSpec{Text: "t"}
	d.frame()
	clear(d.tr.in.Cancelled[:])
	d.flushDrag()
	if len(win.specs) != 2 || d.UI.DragRunning() || d.tr.in.Cancelled[ui.ButtonLeft] || !d.tr.in.Down[ui.ButtonLeft] {
		t.Errorf("refused drag: specs %d running %v in %+v", len(win.specs), d.UI.DragRunning(), d.tr.in.Cancelled)
	}
}

// Without a picture from the app the window gets none: the system draws the
// icon of the first item. (A snapshot of the box would drain the frame's GPU
// work synchronously.)
func TestDragWithoutAPreviewAsksForNone(t *testing.T) {
	d := newDragApp()
	win := &fakeDrag{}
	d.dragT = win
	d.frame()
	d.tr.in.Inside = true
	d.tr.in.MouseX, d.tr.in.MouseY = 40, 30
	d.tr.in.SetButton(ui.ButtonLeft, true, 1)
	d.start = &ui.DragSpec{Text: "t"}
	d.frame()
	d.flushDrag()
	if len(win.specs) != 1 || win.specs[0].Preview != nil {
		t.Errorf("specs %+v", win.specs)
	}
}

// Drops that arrive together are all delivered, one a frame and in order, each
// to the target that accepted it; what does not fit the queue is lost, not
// written over the others.
func TestDropsThatArriveTogetherAreAllDelivered(t *testing.T) {
	d := newDragApp()
	d.frame()
	d.frame()
	_, _, key := d.UI.DropAt(50, 40, ui.DropText, ui.DropAllOps)
	if key == 0 {
		t.Fatal("no target")
	}
	ev := func(txt string) {
		d.onDrag(&platform.Event{Kind: platform.EvDrop, X: 50, Y: 40, Code: uint32(platform.DragText), SelStart: 1, Tag: uint64(key)}, []byte(txt))
	}
	ev("one")
	ev("two")
	ev("three")
	var got []string
	for i := 0; i < 5; i++ {
		d.frame()
		if d.got {
			got = append(got, string(d.drop.Data))
		}
		if i < 2 && !d.tr.pending() {
			t.Fatalf("frame %d: no frame owed for the drops behind", i)
		}
	}
	if len(got) != 3 || got[0] != "one" || got[1] != "two" || got[2] != "three" {
		t.Errorf("delivered %q", got)
	}

	// The queue is bounded: the rest is lost, and what was queued stays intact.
	// (The first goes into the input, maxQueuedDrops wait behind it.)
	for i := 0; i < maxQueuedDrops+5; i++ {
		ev(string(rune('a' + i)))
	}
	if d.tr.drops.lost != 4 {
		t.Errorf("%d lost, want 4", d.tr.drops.lost)
	}
	got = got[:0]
	for i := 0; i < maxQueuedDrops+3; i++ {
		d.frame()
		if d.got {
			got = append(got, string(d.drop.Data))
		}
	}
	if len(got) != maxQueuedDrops+1 || got[0] != "a" || got[maxQueuedDrops] != string(rune('a'+maxQueuedDrops)) {
		t.Errorf("delivered %q", got)
	}

	// A queued drop is bounded in bytes too: it fails rather than grow the queue past a paste.
	big := bytes.Repeat([]byte("x"), ui.MaxKeptPaste)
	ev("first")
	ev(string(big))
	ev("tail")
	d.frame()
	d.frame()
	if !d.got || d.drop.Failed || len(d.drop.Data) != ui.MaxKeptPaste {
		t.Fatalf("second: got %v failed %v, %d bytes", d.got, d.drop.Failed, len(d.drop.Data))
	}
	d.frame()
	if !d.got || !d.drop.Failed || len(d.drop.Data) != 0 {
		t.Errorf("a drop that would take the queue past MaxKeptPaste: got %v %+v", d.got, d.drop)
	}
}

// The target that accepted the drop goes with it into the input.
func TestDropCarriesItsTarget(t *testing.T) {
	d := newDragApp()
	d.frame()
	d.frame()
	ans := d.dragAnswer(50, 40, platform.DragText, platform.DragOpAll)
	if ans.Target == 0 || ans.Op == platform.DragOpNone {
		t.Fatalf("answer %+v", ans)
	}
	d.onDrag(&platform.Event{Kind: platform.EvDrop, X: 300, Y: 250, Code: uint32(platform.DragText), SelStart: 1, Tag: ans.Target}, []byte("late"))
	if d.tr.in.Drop.Target != ui.Key(ans.Target) {
		t.Errorf("target %v, want %v", d.tr.in.Drop.Target, ans.Target)
	}
	d.frame() // the pointer is nowhere near the box, the drop is the box's anyway
	if !d.got || string(d.drop.Data) != "late" {
		t.Errorf("got %v %+v", d.got, d.drop)
	}
}

// A frame with drop targets and no drag costs the window one comparison.
func TestFlushDragAllocatesNothing(t *testing.T) {
	d := newDragApp()
	d.dragT = &nopDrag{}
	d.frame()
	d.frame()
	if n := testing.AllocsPerRun(100, func() {
		d.frame()
		d.flushDrag()
	}); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}

type nopDrag struct{}

func (nopDrag) SetDropKinds(platform.DragKinds) {}
func (nopDrag) StartDrag(*platform.DragSpec) (platform.DragTicket, error) {
	return 0, errors.New("no")
}
