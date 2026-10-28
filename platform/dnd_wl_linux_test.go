//go:build linux

package platform

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Drag and drop on Wayland, against the scripted compositor (fakewl_linux_test.go):
// it plays the other application's drag over the window with the data-device
// events, and the target that the window's own drag goes to.

// wlDragSeen collects the drag events of a window while it is pumped.
type wlDragSeen struct {
	w     *Window
	drags []xdrag
}

// step pumps the window once and notes its drag events.
func (s *wlDragSeen) step() {
	for _, e := range s.w.Wait(int64(5 * time.Millisecond)) {
		switch e.Kind {
		case EvDragEnter, EvDragMove, EvDragLeave, EvDrop, EvDragEnd:
			d := xdrag{kind: e.Kind, code: e.Code, failed: e.Flags&FlagFailed != 0, tag: e.Tag, x: e.X, y: e.Y, items: e.SelStart}
			if e.Kind == EvDrop && e.Text.Len > 0 {
				d.text = string(s.w.Text(e.Text))
			}
			s.drags = append(s.drags, d)
		}
	}
}

func (s *wlDragSeen) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); s.step() {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (s *wlDragSeen) count(kind EventKind) (n int) {
	for _, d := range s.drags {
		if d.kind == kind {
			n++
		}
	}
	return n
}

func (s *wlDragSeen) last(kind EventKind) xdrag {
	for i := len(s.drags) - 1; i >= 0; i-- {
		if s.drags[i].kind == kind {
			return s.drags[i]
		}
	}
	return xdrag{}
}

// wlDndWindow opens a window on the fake compositor that takes kinds and
// answers with op, and gives it the pointer.
func wlDndWindow(t *testing.T, kinds DragKinds, op, ops DragOp, accept DragKinds) (*fakeWL, *wlDragSeen) {
	t.Helper()
	f, w := focused(t, "")
	w.SetDragHandler(func(x, y float32, offered DragKinds, allowed DragOp) DragAnswer {
		return DragAnswer{Op: Pick(op, allowed), Accept: accept, Target: 9}
	})
	w.SetDropKinds(kinds)
	f.do("pointer 100 100")
	s := &wlDragSeen{w: w}
	s.until(t, "the pointer", func() bool { return w.ptrIn })
	return f, s
}

func TestWaylandDndTargetTakesText(t *testing.T) {
	f, s := wlDndWindow(t, DragText|DragFiles, DragOpCopy, DragOpCopy, DragAny)
	f.do(`dnd-enter 20 30 text/plain;charset=utf-8,UTF8_STRING 3 dropped\r\ntext`)
	s.until(t, "EvDragEnter", func() bool { return s.count(EvDragEnter) == 1 })
	if e := s.last(EvDragEnter); DragKinds(e.code) != DragText || e.x != 20 || e.y != 30 {
		t.Fatalf("enter = %+v, want text at (20, 30)", e)
	}
	// The window accepts the text type and the copy action.
	if l := f.wait("REQ wl_data_offer.accept", 5*time.Second); !strings.Contains(l, `"text/plain;charset=utf-8"`) {
		t.Fatalf("accept = %q, want the utf-8 text type", l)
	}
	if l := f.wait("REQ wl_data_offer.set_actions", 5*time.Second); l != "REQ wl_data_offer.set_actions 1 1" {
		t.Fatalf("set_actions = %q, want copy", l)
	}

	f.do("dnd-motion 25 36")
	s.until(t, "EvDragMove", func() bool { return s.count(EvDragMove) == 1 })
	if m := s.last(EvDragMove); m.x != 25 || m.y != 36 || m.tag != s.last(EvDragEnter).tag {
		t.Fatalf("move = %+v", m)
	}

	f.do("dnd-drop")
	s.until(t, "EvDrop", func() bool { return s.count(EvDrop) == 1 })
	if d := s.last(EvDrop); d.failed || DragKinds(d.code) != DragText || d.text != "dropped\ntext" || d.tag != 9 {
		t.Fatalf("drop = %+v", d)
	}
	f.wait("REQ wl_data_offer.finish", 5*time.Second)
	f.wait("REQ wl_data_offer.destroy", 5*time.Second)
	if s.count(EvDragLeave) != 0 {
		t.Fatalf("%d EvDragLeave after a drop", s.count(EvDragLeave))
	}
}

func TestWaylandDndTargetTakesFilesAndImages(t *testing.T) {
	f, s := wlDndWindow(t, DragFiles|DragText, DragOpCopy, DragOpCopy, DragAny)
	f.do(`dnd-enter 5 5 text/plain,text/uri-list,image/png 1 # list\r\nfile:///tmp/a%%20b.txt\r\nfile:///var/x\r\n`)
	f.do("dnd-drop")
	s.until(t, "EvDrop", func() bool { return s.count(EvDrop) == 1 })
	if d := s.last(EvDrop); d.failed || DragKinds(d.code) != DragFiles || d.text != "/tmp/a b.txt\x00/var/x\x00" || d.items != 2 {
		t.Fatalf("files drop = %+v", d)
	}

	// The window takes images too: they are fetched as image/png, whole.
	s.w.SetDropKinds(DragImage)
	png, _ := testPNG(t, 4, 4)
	f.do("dnd-enter 5 5 image/png 1 hex:%x", png)
	f.do("dnd-drop")
	s.until(t, "EvDrop", func() bool { return s.count(EvDrop) == 2 })
	if d := s.last(EvDrop); d.failed || DragKinds(d.code) != DragImage || d.text != string(png) {
		t.Fatalf("image drop = %+v", d)
	}
}

func TestWaylandDndTargetRefusesAndLeaves(t *testing.T) {
	f, s := wlDndWindow(t, DragText, DragOpCopy, DragOpCopy, 0)
	f.do("dnd-enter 5 5 text/plain 3 nope")
	s.until(t, "EvDragEnter", func() bool { return s.count(EvDragEnter) == 1 })
	if l := f.wait("REQ wl_data_offer.accept", 5*time.Second); !strings.HasSuffix(l, ` ""`) {
		t.Fatalf("a refusing handler: accept = %q, want no type", l)
	}
	if l := f.wait("REQ wl_data_offer.set_actions", 5*time.Second); l != "REQ wl_data_offer.set_actions 0 0" {
		t.Fatalf("set_actions = %q, want none", l)
	}
	f.do("dnd-drop")
	s.until(t, "EvDragLeave", func() bool { return s.count(EvDragLeave) == 1 })
	if s.count(EvDrop) != 0 {
		t.Fatal("a refused drag was dropped")
	}

	// A drag that leaves, and one of a kind the window does not take.
	s.w.SetDragHandler(func(float32, float32, DragKinds, DragOp) DragAnswer {
		return DragAnswer{Op: DragOpCopy, Accept: DragAny}
	})
	f.do("dnd-enter 5 5 text/plain 3 x")
	s.until(t, "EvDragEnter", func() bool { return s.count(EvDragEnter) == 2 })
	f.do("dnd-leave")
	s.until(t, "EvDragLeave", func() bool { return s.count(EvDragLeave) == 2 })
	n := len(s.drags)
	f.do("dnd-enter 5 5 image/png 3 x")
	s.until(t, "the refusal", func() bool { return f.seen("REQ wl_data_offer.accept") })
	for i := 0; i < 5; i++ {
		s.step()
	}
	if len(s.drags) != n {
		t.Fatalf("events for an image drag on a text window: %+v", s.drags[n:])
	}
}

func TestWaylandDndTargetTimesOut(t *testing.T) {
	f, s := wlDndWindow(t, DragText, DragOpCopy, DragOpCopy, DragText)
	f.do("dnd-enter 5 5 text/plain 3 STALL") // a source that writes half and never finishes
	s.until(t, "EvDragEnter", func() bool { return s.count(EvDragEnter) == 1 })
	f.do("dnd-drop")
	s.until(t, "the receive", func() bool { return s.w.wy.dnd.recv })
	s.w.wy.dnd.deadline = nanotime() + int64(100*time.Millisecond)
	s.until(t, "a failed EvDrop", func() bool { return s.count(EvDrop) == 1 })
	if !s.last(EvDrop).failed {
		t.Fatalf("drop = %+v, want failed", s.last(EvDrop))
	}
	f.wait("REQ wl_data_offer.destroy", 5*time.Second)
	if f.seen("REQ wl_data_offer.finish") {
		t.Fatal("a failed drop was finished")
	}
}

func TestWaylandDndSourceDropsOnTheCompositor(t *testing.T) {
	f, s := wlDndWindow(t, 0, DragOpCopy, DragOpCopy, 0)
	w := s.w
	f.do("button 272 1 0") // a left press
	s.until(t, "the press", func() bool { return w.leftDown })
	spec := &DragSpec{Items: []DragItem{{Text: "héllo"}, {Path: "/tmp/x y.txt"}}, Ops: DragOpCopy | DragOpMove}
	ticket, err := w.StartDrag(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartDrag(spec); err == nil {
		t.Fatal("a second drag started while one runs")
	}
	var offered []string
	for {
		l, ok := f.tryWait("REQ wl_data_source.offer", 200*time.Millisecond)
		if !ok {
			break
		}
		offered = append(offered, l)
	}
	if len(offered) != 4 || !strings.Contains(offered[0], "text/uri-list") || !strings.Contains(offered[1], "text/plain;charset=utf-8") {
		t.Fatalf("offered %q; want the uri list and three text types", offered)
	}
	if l := f.wait("REQ wl_data_source.set_actions", 5*time.Second); l != "REQ wl_data_source.set_actions 3" {
		t.Fatalf("set_actions = %q, want copy and move", l)
	}
	if l := f.wait("REQ wl_data_device.start_drag", 5*time.Second); !strings.HasSuffix(l, " 0 "+fmt.Sprint(wlSerialOf(f, t))) {
		t.Fatalf("start_drag = %q; want no icon and the press's serial", l)
	}
	if w.leftDown {
		t.Fatal("the press is still held after the drag started")
	}

	// The target asks for the data, the user drops it.
	f.do("drag-target text/plain;charset=utf-8")
	f.do("drag-send text/uri-list")
	s.until(t, "the uri list to be sent", func() bool { return f.seen("PASTED") })
	if l, want := f.wait("PASTED", time.Second), fmt.Sprintf("PASTED %d %s", len("file:///tmp/x%20y.txt\r\n"), fnv32("file:///tmp/x%20y.txt\r\n")); l != want {
		t.Fatalf("%q, want %q", l, want)
	}
	f.do("drag-send UTF8_STRING")
	s.until(t, "the text to be sent", func() bool { return f.seen("PASTED") })
	if l, want := f.wait("PASTED", time.Second), fmt.Sprintf("PASTED %d %s", len("héllo"), fnv32("héllo")); l != want {
		t.Fatalf("%q, want %q", l, want)
	}
	f.do("drag-action 1")
	f.do("drag-drop")
	f.do("drag-finish")
	s.until(t, "EvDragEnd", func() bool { return s.count(EvDragEnd) == 1 })
	if e := s.last(EvDragEnd); e.tag != uint64(ticket) || DragOp(e.code) != DragOpCopy {
		t.Fatalf("end = %+v, want ticket %d and copy", e, ticket)
	}
	s.until(t, "the source to be destroyed", func() bool { return f.seen("REQ wl_data_source.destroy") })
	if _, err := w.StartDrag(spec); err != ErrNoPress {
		t.Fatalf("after the drag: %v, want ErrNoPress", err)
	}
}

// wlSerialOf is the serial of the latest button the compositor sent.
func wlSerialOf(f *fakeWL, t *testing.T) uint32 {
	t.Helper()
	l := f.wait("SERIAL", time.Second)
	var n uint32
	fmt.Sscanf(l, "SERIAL %d", &n)
	return n
}

func TestWaylandDndSourceCancelled(t *testing.T) {
	f, s := wlDndWindow(t, 0, DragOpCopy, DragOpCopy, 0)
	w := s.w
	f.do("button 272 1 0")
	s.until(t, "the press", func() bool { return w.leftDown })
	ticket, err := w.StartDrag(&DragSpec{Items: []DragItem{{Text: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	f.wait("REQ wl_data_device.start_drag", 5*time.Second)
	f.do("drag-cancel")
	s.until(t, "EvDragEnd", func() bool { return s.count(EvDragEnd) == 1 })
	if e := s.last(EvDragEnd); e.tag != uint64(ticket) || DragOp(e.code) != DragOpNone {
		t.Fatalf("end = %+v, want ticket %d and no operation", e, ticket)
	}
}

func TestWaylandDndStartDragRefusals(t *testing.T) {
	_, s := wlDndWindow(t, 0, DragOpCopy, DragOpCopy, 0)
	w := s.w
	if _, err := w.StartDrag(&DragSpec{Items: []DragItem{{Text: "x"}}}); err != ErrNoPress {
		t.Errorf("no press held: %v", err)
	}
	w.leftDown = true // (a press the compositor never sent has no serial)
	if _, err := w.StartDrag(&DragSpec{Items: []DragItem{{Text: "x"}}}); err != ErrNoPress {
		t.Errorf("a press without a serial: %v", err)
	}
}

func TestWlOfferKindsAndMimes(t *testing.T) {
	text, png, uri := uint8(1<<1), uint8(1<<wlPNG), uint8(1<<wlURI)
	if k := wlOfferKinds(text | png | uri); k != DragAny {
		t.Errorf("kinds = %d", k)
	}
	if wlOfferKinds(0) != 0 || wlOfferKinds(1<<wlHint) != 0 {
		t.Error("the password hint is no kind")
	}
	if i := wlOfferMime(1<<2|1<<1, DragText); i != 1 {
		t.Errorf("text mime = %d, want UTF8_STRING (the first listed in our order)", i)
	}
	if wlOfferMime(text, DragFiles) != -1 || wlOfferMime(png, DragImage) != wlPNG || wlOfferMime(uri, DragFiles) != wlURI {
		t.Error("mime for a kind")
	}
	if wlOpsOfActions(0) != DragOpCopy || wlOpsOfActions(3) != DragOpCopy|DragOpMove || wlOpsOfActions(4) != DragOpCopy {
		t.Error("ops of actions")
	}
	if wlActionOfOp(DragOpMove) != 2 || wlActionOfOp(DragOpLink) != 0 {
		t.Error("action of op")
	}
}
